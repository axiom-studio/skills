package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ToolSnapshot struct {
	Tool map[string]interface{} `json:"tool"`
	Hash string                 `json:"hash"`
}
type mcpSession struct {
	r       *Runtime
	token   string
	id      string
	version string
	seq     int
}

func (s *mcpSession) send(ctx context.Context, method string, params interface{}, notification bool) (map[string]interface{}, error) {
	s.seq++
	msg := map[string]interface{}{"jsonrpc": "2.0", "method": method, "params": params}
	if !notification {
		msg["id"] = s.seq
	}
	data, err := json.Marshal(msg)
	if err != nil || len(data) > MaxBytes {
		return nil, fmt.Errorf("invalid or oversized MCP request")
	}
	headers := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json, text/event-stream"}}
	if s.id != "" {
		headers.Set("Mcp-Session-Id", s.id)
	}
	if s.version != "" {
		headers.Set("MCP-Protocol-Version", s.version)
	}
	target := s.r.Profile.Endpoint
	if len(s.r.Profile.FixedQuery) > 0 {
		q := url.Values{}
		for k, v := range s.r.Profile.FixedQuery {
			q.Set(k, v)
		}
		target += "?" + q.Encode()
	}
	resp, err := s.r.request(ctx, "POST", target, bytes.NewReader(data), s.token, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if method == "initialize" {
		s.id = resp.Header.Get("Mcp-Session-Id")
		if len(s.id) > 256 {
			return nil, fmt.Errorf("invalid MCP session ID")
		}
		for _, c := range s.id {
			if c < 33 || c > 126 {
				return nil, fmt.Errorf("invalid MCP session ID")
			}
		}
	}
	if notification {
		if resp.StatusCode != 202 {
			return nil, fmt.Errorf("MCP notification was not accepted")
		}
		return nil, nil
	}
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if contentType == "text/event-stream" {
		// Stop on the matching response without waiting for the server to close the stream.
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, MaxBytes+1))
		scanner.Buffer(make([]byte, 4096), MaxBytes)
		var event []string
		total := 0
		for scanner.Scan() {
			line := scanner.Text()
			total += len(line) + 1
			if total > MaxBytes {
				return nil, fmt.Errorf("MCP stream exceeds size limit")
			}
			if line == "" {
				if len(event) > 0 {
					result, done, err := rpcResponse([]byte(strings.Join(event, "\n")), s.seq)
					if err != nil {
						return nil, err
					}
					if done {
						return result, nil
					}
				}
				event = nil
			} else if strings.HasPrefix(line, "data:") {
				event = append(event, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		return nil, fmt.Errorf("MCP stream ended without a response; outcome may be unknown")
	}
	if contentType != "application/json" {
		return nil, fmt.Errorf("unsupported MCP response content type")
	}
	b, err := readBounded(resp.Body)
	if err != nil {
		return nil, err
	}
	result, done, err := rpcResponse(b, s.seq)
	if err != nil {
		return nil, err
	}
	if !done {
		return nil, fmt.Errorf("MCP response ID missing")
	}
	return result, nil
}
func rpcResponse(b []byte, id int) (map[string]interface{}, bool, error) {
	var msg struct {
		JSONRPC string                 `json:"jsonrpc"`
		ID      *int                   `json:"id"`
		Method  string                 `json:"method"`
		Result  map[string]interface{} `json:"result"`
		Error   *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &msg) != nil || msg.JSONRPC != "2.0" {
		return nil, false, fmt.Errorf("invalid MCP JSON-RPC envelope")
	}
	if msg.Method != "" {
		if msg.ID != nil {
			return nil, false, fmt.Errorf("server-initiated MCP requests are not supported")
		}
		return nil, false, nil
	}
	if msg.ID == nil || *msg.ID != id {
		return nil, false, fmt.Errorf("MCP response ID mismatch")
	}
	if msg.Error != nil {
		return nil, false, fmt.Errorf("MCP protocol error %d", msg.Error.Code)
	}
	if msg.Result == nil {
		return nil, false, fmt.Errorf("MCP response missing result")
	}
	return msg.Result, true, nil
}
func (r *Runtime) startMCP(ctx context.Context, token string) (*mcpSession, error) {
	s := &mcpSession{r: r, token: token}
	result, err := s.send(ctx, "initialize", map[string]interface{}{"protocolVersion": "2025-11-25", "capabilities": map[string]interface{}{}, "clientInfo": map[string]interface{}{"name": "axiom-generic-mcp", "version": RuntimeVersion}}, false)
	if err != nil {
		return nil, err
	}
	s.version, _ = result["protocolVersion"].(string)
	switch s.version {
	case "2025-03-26", "2025-06-18", "2025-11-25":
	default:
		return nil, fmt.Errorf("unsupported negotiated MCP version")
	}
	caps, _ := result["capabilities"].(map[string]interface{})
	if _, ok := caps["tools"]; !ok {
		return nil, fmt.Errorf("MCP server does not advertise tools")
	}
	_, err = s.send(ctx, "notifications/initialized", map[string]interface{}{}, true)
	if err != nil {
		return nil, err
	}
	return s, nil
}
func (s *mcpSession) close() {
	if s.id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	h := http.Header{"Mcp-Session-Id": {s.id}, "Mcp-Protocol-Version": {s.version}}
	target := s.r.Profile.Endpoint
	if len(s.r.Profile.FixedQuery) > 0 {
		q := url.Values{}
		for k, v := range s.r.Profile.FixedQuery {
			q.Set(k, v)
		}
		target += "?" + q.Encode()
	}
	resp, err := s.r.request(ctx, "DELETE", target, nil, s.token, h)
	if err == nil {
		resp.Body.Close()
	}
}
func (s *mcpSession) list(ctx context.Context) ([]ToolSnapshot, error) {
	result := []ToolSnapshot{}
	cursor := ""
	seen := map[string]bool{}
	names := map[string]bool{}
	for page := 0; page < 20; page++ {
		params := map[string]interface{}{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		response, err := s.send(ctx, "tools/list", params, false)
		if err != nil {
			return nil, err
		}
		tools, ok := response["tools"].([]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid tools/list response")
		}
		for _, item := range tools {
			tool, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("invalid MCP tool")
			}
			name, _ := tool["name"].(string)
			if name == "" || names[name] {
				return nil, fmt.Errorf("missing or duplicate MCP tool name")
			}
			names[name] = true
			result = append(result, ToolSnapshot{Tool: tool, Hash: Digest(tool)})
			if len(result) > 1000 {
				return nil, fmt.Errorf("MCP discovery exceeds tool limit")
			}
		}
		next, exists := response["nextCursor"]
		if !exists {
			return result, nil
		}
		var valid bool
		cursor, valid = next.(string)
		if !valid || cursor == "" || seen[cursor] {
			return nil, fmt.Errorf("invalid or repeating MCP pagination cursor")
		}
		seen[cursor] = true
	}
	return nil, fmt.Errorf("MCP discovery exceeds page limit")
}
func (r *Runtime) Discover(ctx context.Context, token string) ([]ToolSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	s, err := r.startMCP(ctx, token)
	if err != nil {
		return nil, err
	}
	defer s.close()
	snapshots, err := s.list(ctx)
	if err != nil {
		return nil, err
	}
	for i := range snapshots {
		snapshots[i].Tool = redactValue(snapshots[i].Tool, token, false).(map[string]interface{})
	}
	return snapshots, nil
}
func (r *Runtime) callMCP(ctx context.Context, op Operation, args map[string]interface{}, token string) (map[string]interface{}, error) {
	s, err := r.startMCP(ctx, token)
	if err != nil {
		return nil, err
	}
	defer s.close()
	tools, err := s.list(ctx)
	if err != nil {
		return nil, err
	}
	var tool map[string]interface{}
	for _, snapshot := range tools {
		if snapshot.Tool["name"] == op.Tool {
			if snapshot.Hash != op.ToolHash {
				return nil, fmt.Errorf("MCP tool definition changed; rediscover and rebuild block")
			}
			tool = snapshot.Tool
			break
		}
	}
	if tool == nil {
		return nil, fmt.Errorf("pinned MCP tool is unavailable")
	}
	input, _ := tool["inputSchema"].(map[string]interface{})
	if err := validateValue(input, args); err != nil {
		return nil, fmt.Errorf("remote input schema unsupported or arguments invalid")
	}
	output, outputOK := tool["outputSchema"].(map[string]interface{})
	if _, exists := tool["outputSchema"]; exists && !outputOK {
		return nil, fmt.Errorf("invalid remote output schema")
	}
	if output != nil {
		if _, err := compileSchema(output); err != nil {
			return nil, fmt.Errorf("remote output schema unsupported")
		}
	}
	if execution, ok := tool["execution"].(map[string]interface{}); ok && execution["taskSupport"] == "required" {
		return nil, fmt.Errorf("task-required MCP tools are not supported")
	}
	result, err := s.send(ctx, "tools/call", map[string]interface{}{"name": op.Tool, "arguments": args}, false)
	if err != nil {
		return nil, err
	}
	if flag, exists := result["isError"]; exists {
		b, ok := flag.(bool)
		if !ok {
			return nil, fmt.Errorf("invalid MCP isError")
		}
		if b {
			return nil, fmt.Errorf("MCP tool execution failed; outcome may be partial; no automatic retry")
		}
	}
	if _, ok := result["content"].([]interface{}); !ok {
		return nil, fmt.Errorf("MCP result is missing content array; execution may already have completed")
	}
	if output != nil {
		if err := validateValue(output, result["structuredContent"]); err != nil {
			return nil, fmt.Errorf("MCP output schema mismatch; execution may already have completed")
		}
	}
	if op.OutputSchema != nil {
		if err := validateValue(op.OutputSchema, result["structuredContent"]); err != nil {
			return nil, fmt.Errorf("pinned output schema mismatch; execution may already have completed")
		}
	}
	return map[string]interface{}{"data": redact(result, token)}, nil
}
