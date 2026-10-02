package integration

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// A local server runs in the tenant's skill container, never on the user's
// desktop. The host must explicitly provision a runtime with local execution.
func (s *mcpSession) startProcess(ctx context.Context) error {
	if os.Getenv("INTEGRATION_STDIO_ALLOWED") != "1" {
		return fmt.Errorf("this runtime has no local MCP execution environment; provision a tenant-scoped stdio runtime or use a remote server")
	}
	m := s.r.Profile.MCP
	s.process = exec.CommandContext(ctx, m.Command, m.Args...)
	s.process.Dir = m.Directory
	s.process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	s.process.Cancel = func() error {
		if s.process.Process == nil {
			return nil
		}
		return syscall.Kill(-s.process.Process.Pid, syscall.SIGKILL)
	}
	// Do not pass the serving process's credentials or environment to a server.
	s.process.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp", "TMPDIR=/tmp"}
	values := mcpSecrets(s.token)
	for _, v := range m.Environment {
		value, exists := values[v.Field]
		if !exists || strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid environment value")
		}
		s.process.Env = append(s.process.Env, v.Name+"="+value)
	}
	out, e := s.process.StdoutPipe()
	if e != nil {
		return fmt.Errorf("could not open MCP stdout")
	}
	s.input, e = s.process.StdinPipe()
	if e != nil {
		return fmt.Errorf("could not open MCP stdin")
	}
	s.process.Stderr = io.Discard
	if e = s.process.Start(); e != nil {
		return fmt.Errorf("MCP command could not start; executable must be installed in the skill container")
	}
	s.messages = make(chan []byte, 1)
	go func() {
		defer close(s.messages)
		scan := bufio.NewScanner(out)
		scan.Buffer(make([]byte, 4096), MaxBytes)
		for scan.Scan() {
			b := append([]byte(nil), scan.Bytes()...)
			select {
			case s.messages <- b:
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}
func (s *mcpSession) sendProcess(ctx context.Context, data []byte, notification bool) (map[string]interface{}, error) {
	if _, e := s.input.Write(append(data, '\n')); e != nil {
		return nil, fmt.Errorf("MCP process write failed; outcome may be unknown")
	}
	if notification {
		return nil, nil
	}
	return s.readMessage(ctx)
}
func (s *mcpSession) readMessage(ctx context.Context) (map[string]interface{}, error) {
	for n := 0; n < 1000; n++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("MCP response deadline exceeded; outcome may be unknown")
		case raw, ok := <-s.messages:
			if !ok {
				return nil, fmt.Errorf("MCP stream ended without a response")
			}
			result, done, e := rpcResponse(raw, s.seq)
			if e != nil {
				return nil, e
			}
			if done {
				return result, nil
			}
		}
	}
	return nil, fmt.Errorf("too many MCP notifications")
}
func (s *mcpSession) startSSE(ctx context.Context) error {
	target := s.target()
	resp, e := s.r.request(ctx, "GET", target, nil, s.token, http.Header{"Accept": {"text/event-stream"}})
	if e != nil {
		return e
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body.Close()
		return fmt.Errorf("legacy MCP endpoint did not return SSE")
	}
	s.stream = resp.Body
	s.messages = make(chan []byte, 1)
	endpoints := make(chan string, 1)
	go func() {
		defer close(s.messages)
		defer close(endpoints)
		scan := bufio.NewScanner(resp.Body)
		scan.Buffer(make([]byte, 4096), MaxBytes)
		event := ""
		data := []string{}
		size := 0
		for scan.Scan() {
			line := scan.Text()
			size += len(line)
			if size > MaxBytes {
				return
			}
			if line == "" {
				if len(data) > 0 {
					value := strings.Join(data, "\n")
					if event == "endpoint" {
						select {
						case endpoints <- value:
						case <-ctx.Done():
							return
						}
					} else if event == "message" || event == "" {
						select {
						case s.messages <- []byte(value):
						case <-ctx.Done():
							return
						}
					}
				}
				event = ""
				data = nil
				size = 0
			} else if strings.HasPrefix(line, "event:") {
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			} else if strings.HasPrefix(line, "data:") {
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
	}()
	select {
	case <-ctx.Done():
		resp.Body.Close()
		return fmt.Errorf("MCP SSE endpoint discovery timed out")
	case endpoint, ok := <-endpoints:
		if !ok {
			return fmt.Errorf("MCP SSE stream has no message endpoint")
		}
		base, e := url.Parse(target)
		if e != nil {
			return e
		}
		relative, e := url.Parse(endpoint)
		if e != nil {
			return e
		}
		resolved := base.ResolveReference(relative)
		if resolved.Scheme != base.Scheme || resolved.Host != base.Host || resolved.User != nil || resolved.Fragment != "" {
			resp.Body.Close()
			return fmt.Errorf("MCP SSE message endpoint must remain on the pinned origin")
		}
		s.postEndpoint = resolved.String()
	}
	return nil
}
func (s *mcpSession) sendSSE(ctx context.Context, data []byte, notification bool) (map[string]interface{}, error) {
	resp, e := s.r.request(ctx, "POST", s.postEndpoint, strings.NewReader(string(data)), s.token, http.Header{"Content-Type": {"application/json"}})
	if e != nil {
		return nil, e
	}
	resp.Body.Close()
	if notification {
		return nil, nil
	}
	return s.readMessage(ctx)
}
func (s *mcpSession) target() string {
	target := s.r.Profile.Endpoint
	if len(s.r.Profile.FixedQuery) > 0 {
		q := url.Values{}
		for k, v := range s.r.Profile.FixedQuery {
			q.Set(k, v)
		}
		target += "?" + q.Encode()
	}
	return target
}
