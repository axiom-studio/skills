package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Dial the addresses we validated, not a hostname that can be rebound after validation.
// Private services need a separately governed network adapter; callers cannot opt out.
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}
func NewHTTPClient() *http.Client {
	t := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second, MaxResponseHeaderBytes: 32 << 10, DisableKeepAlives: true}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid destination")
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("destination lookup failed")
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, fmt.Errorf("non-public destination blocked")
			}
		}
		dialer := net.Dialer{Timeout: 10 * time.Second}
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("destination connection failed")
	}
	return &http.Client{Transport: t, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func readBounded(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("response read failed; execution outcome may be unknown")
	}
	if len(b) > MaxBytes {
		return nil, fmt.Errorf("response too large; execution outcome may be unknown")
	}
	return b, nil
}
func (r *Runtime) request(ctx context.Context, method, target string, body io.Reader, token string, headers http.Header) (*http.Response, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("invalid request destination")
	}
	base, _ := url.Parse(r.Profile.Endpoint)
	if u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil {
		return nil, fmt.Errorf("request destination differs from pinned origin")
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("invalid request")
	}
	req.Header = headers.Clone()
	if c := r.Profile.Credential; c != nil {
		if token == "" || strings.ContainsAny(token, "\r\n") {
			return nil, fmt.Errorf("missing or invalid credential binding")
		}
		req.Header.Set(c.Header, c.Prefix+token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed; execution outcome may be unknown; do not blindly replay writes")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("upstream HTTP %d; no automatic retry", resp.StatusCode)
	}
	return resp, nil
}
func scalar(v interface{}) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case float64, json.Number:
		return fmt.Sprint(x), nil
	default:
		return "", fmt.Errorf("path/query values must be strings, numbers, or booleans")
	}
}
func (r *Runtime) callAPI(ctx context.Context, op Operation, args map[string]interface{}, token string) (map[string]interface{}, error) {
	path := op.Path
	for name, param := range op.PathParams {
		v, ok := args[param]
		if !ok {
			return nil, fmt.Errorf("missing path argument")
		}
		s, err := scalar(v)
		if err != nil {
			return nil, err
		}
		// Prevent upstream path normalization from escaping the selected resource.
		if s == "" || strings.ContainsAny(s, "/\\%?#") || s == "." || s == ".." {
			return nil, fmt.Errorf("unsafe path argument")
		}
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(s))
	}
	q := url.Values{}
	for k, v := range r.Profile.FixedQuery {
		q.Set(k, v)
	}
	for name, param := range op.QueryParams {
		if v, ok := args[param]; ok {
			s, err := scalar(v)
			if err != nil {
				return nil, err
			}
			q.Set(name, s)
		}
	}
	target := strings.TrimRight(r.Profile.Endpoint, "/") + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	var body io.Reader
	contentType := "application/json"
	if op.BodyParam != "" {
		if v, ok := args[op.BodyParam]; ok {
			var b []byte
			switch op.BodyEncoding {
			case "form":
				contentType = "application/x-www-form-urlencoded"
				values, ok := v.(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("form body must be an object")
				}
				form := url.Values{}
				for k, value := range values {
					s, err := scalar(value)
					if err != nil {
						return nil, err
					}
					form.Set(k, s)
				}
				b = []byte(form.Encode())
			case "text":
				contentType = "text/plain"
				value, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("text body must be a string")
				}
				b = []byte(value)
			default:
				var err error
				b, err = json.Marshal(v)
				if err != nil {
					return nil, fmt.Errorf("invalid request body")
				}
			}
			if len(b) > MaxBytes {
				return nil, fmt.Errorf("request body too large")
			}
			body = strings.NewReader(string(b))
		}
	}
	accept := "application/json"
	if op.ResponseEncoding == "text" {
		accept = "text/plain"
	}
	resp, err := r.request(ctx, op.Method, target, body, token, http.Header{"Content-Type": {contentType}, "Accept": {accept}})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := readBounded(resp.Body)
	if err != nil {
		return nil, err
	}
	var result interface{}
	if op.ResponseEncoding == "text" {
		result = string(data)
	} else if len(data) > 0 {
		if err = json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("expected JSON response; execution may already have completed")
		}
	}
	if op.OutputSchema != nil {
		if err = validateValue(op.OutputSchema, result); err != nil {
			return nil, fmt.Errorf("response schema mismatch; execution may already have completed")
		}
	}
	return map[string]interface{}{"status": resp.StatusCode, "data": redact(result, token)}, nil
}
func redact(v interface{}, token string) interface{} { return redactValue(v, token, true) }

func redactValue(v interface{}, token string, maskSecretKeys bool) interface{} {
	switch x := v.(type) {
	case string:
		if token != "" {
			return strings.ReplaceAll(x, token, "[REDACTED]")
		}
		return x
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, y := range x {
			out[i] = redactValue(y, token, maskSecretKeys)
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, y := range x {
			value := redactValue(y, token, maskSecretKeys)
			if maskSecretKeys {
				switch strings.ToLower(k) {
				case "authorization", "password", "secret", "token", "access_token", "refresh_token", "api_key", "apikey":
					value = "[REDACTED]"
				}
			}
			key := k
			if token != "" {
				key = strings.ReplaceAll(k, token, "[REDACTED]")
			}
			out[key] = value
		}
		return out
	default:
		return v
	}
}
