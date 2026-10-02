package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MCPOptions is host-owned configuration. Every field naming a secret refers
// to a key inside the encrypted integration-settings bundle, never a value.
type MCPOptions struct {
	ValuesBinding   string             `json:"valuesBinding,omitempty"`
	Transport       string             `json:"transport"`
	Command         string             `json:"command,omitempty"`
	Args            []string           `json:"args,omitempty"`
	Directory       string             `json:"directory,omitempty"`
	Environment     []MCPSecretValue   `json:"environment,omitempty"`
	Headers         []MCPSecretValue   `json:"headers,omitempty"`
	Authentication  *MCPAuthentication `json:"authentication,omitempty"`
	TimeoutSeconds  int                `json:"timeoutSeconds,omitempty"`
	ProtocolVersion string             `json:"protocolVersion,omitempty"`
	ClientName      string             `json:"clientName,omitempty"`
	ClientVersion   string             `json:"clientVersion,omitempty"`
}
type MCPSecretValue struct {
	Name  string `json:"name"`
	Field string `json:"field"`
}
type MCPAuthentication struct {
	ClientAuth string   `json:"clientAuth,omitempty"`
	Kind       string   `json:"kind"`
	Header     string   `json:"header,omitempty"`
	Prefix     string   `json:"prefix,omitempty"`
	Grant      string   `json:"grant,omitempty"`
	TokenURL   string   `json:"tokenUrl,omitempty"`
	ClientID   string   `json:"clientId,omitempty"`
	Scopes     []string `json:"scopes,omitempty"`
	Resource   string   `json:"resource,omitempty"`
}

var headerName = regexp.MustCompile(`^[!#$%&'*+.^_` + "`" + `|~0-9A-Za-z-]+$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func mcpEndpoint(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery {
		return nil, fmt.Errorf("MCP endpoint must be HTTPS without embedded credentials, query, or fragment; configure non-secret query parameters separately")
	}
	return u, nil
}
func (m *MCPOptions) Validate(p *Profile) error {
	if m.ValuesBinding != "" && m.ValuesBinding != "integration-settings" {
		return fmt.Errorf("invalid MCP connection values binding")
	}
	switch m.Transport {
	case "streamable-http", "sse":
		if m.Command != "" || len(m.Args) > 0 || m.Directory != "" || len(m.Environment) > 0 {
			return fmt.Errorf("remote MCP cannot contain local process settings")
		}
	case "stdio":
		if strings.TrimSpace(m.Command) == "" || strings.ContainsAny(m.Command, "\x00\r\n") || p.Endpoint != "" || len(m.Headers) > 0 || len(p.FixedQuery) > 0 || m.Authentication != nil {
			return fmt.Errorf("stdio requires a command and uses environment variables rather than HTTP settings")
		}
	default:
		return fmt.Errorf("unsupported MCP transport")
	}
	if strings.ContainsRune(m.Directory, 0) {
		return fmt.Errorf("invalid MCP working directory")
	}
	for _, arg := range m.Args {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("invalid MCP argument")
		}
	}
	if m.Transport == "sse" && m.ProtocolVersion == "2026-07-28" {
		return fmt.Errorf("legacy SSE requires a legacy protocol version")
	}
	if len(m.Args) > 128 || len(m.Headers) > 64 || len(m.Environment) > 128 {
		return fmt.Errorf("MCP configuration exceeds limits")
	}
	if m.TimeoutSeconds < 0 || m.TimeoutSeconds > 300 {
		return fmt.Errorf("MCP timeout must be 1–300 seconds")
	}
	if m.ProtocolVersion != "" && m.ProtocolVersion != "2025-03-26" && m.ProtocolVersion != "2025-06-18" && m.ProtocolVersion != "2025-11-25" && m.ProtocolVersion != "2026-07-28" && m.ProtocolVersion != "2024-11-05" {
		return fmt.Errorf("unsupported MCP protocol version")
	}
	if len(m.ClientName) > 128 || len(m.ClientVersion) > 64 {
		return fmt.Errorf("client identity exceeds limits")
	}
	for _, group := range [][]MCPSecretValue{m.Headers, m.Environment} {
		seen := map[string]bool{}
		for _, v := range group {
			key := strings.ToLower(v.Name)
			if v.Field == "" || seen[key] {
				return fmt.Errorf("duplicate or missing MCP value reference")
			}
			seen[key] = true
		}
	}
	for _, v := range m.Headers {
		if !headerName.MatchString(v.Name) || reservedMCPHeader(v.Name) {
			return fmt.Errorf("invalid or protocol-owned MCP header")
		}
	}
	for _, v := range m.Environment {
		if !envName.MatchString(v.Name) {
			return fmt.Errorf("invalid environment variable name")
		}
	}
	if a := m.Authentication; a != nil {
		switch a.Kind {
		case "none", "bearer", "basic":
		case "api-key":
			if !headerName.MatchString(a.Header) || reservedMCPHeader(a.Header) {
				return fmt.Errorf("invalid API key header")
			}
		case "oauth2":
			if _, e := mcpEndpoint(a.TokenURL); e != nil {
				return fmt.Errorf("OAuth token endpoint must be HTTPS")
			}
			if a.ClientAuth != "" && a.ClientAuth != "client_secret_post" && a.ClientAuth != "client_secret_basic" && a.ClientAuth != "none" {
				return fmt.Errorf("unsupported OAuth client authentication")
			}
			if a.ClientID == "" || (a.Grant != "client_credentials" && a.Grant != "refresh_token") {
				return fmt.Errorf("OAuth requires a client ID and client_credentials or refresh_token grant")
			}
			if a.Resource == "" {
				return fmt.Errorf("OAuth resource must identify this MCP server")
			}
		default:
			return fmt.Errorf("unsupported MCP authentication")
		}
		authHeader := "Authorization"
		if a.Kind == "api-key" {
			authHeader = a.Header
		}
		if a.Kind != "none" {
			for _, h := range m.Headers {
				if strings.EqualFold(h.Name, authHeader) {
					return fmt.Errorf("authentication header is configured twice")
				}
			}
		}
		if strings.ContainsAny(a.Prefix, "\r\n\x00") {
			return fmt.Errorf("invalid authentication prefix")
		}
	}
	if (len(m.Headers)+len(m.Environment) > 0 || (m.Authentication != nil && m.Authentication.Kind != "none")) && p.Credential == nil && m.ValuesBinding != "integration-settings" {
		return fmt.Errorf("MCP secret settings require an integration-access reference")
	}
	return nil
}
func reservedMCPHeader(name string) bool {
	if strings.HasPrefix(strings.ToLower(name), "mcp-param-") {
		return true
	}
	switch strings.ToLower(name) {
	case "host", "connection", "content-length", "content-type", "accept", "mcp-session-id", "mcp-protocol-version", "mcp-method", "mcp-name", "last-event-id", "transfer-encoding":
		return true
	}
	return false
}
func mcpSecrets(raw string) map[string]string {
	var values map[string]string
	if json.Unmarshal([]byte(raw), &values) != nil || values == nil {
		values = map[string]string{"access": raw}
	}
	return values
}
func secretValue(values map[string]string, field string) (string, error) {
	v := values[field]
	if v == "" || strings.ContainsAny(v, "\r\n\x00") {
		return "", fmt.Errorf("required MCP secret is missing or invalid")
	}
	return v, nil
}
func (r *Runtime) mcpHeaders(ctx context.Context, raw string) (http.Header, error) {
	h := http.Header{}
	m := r.Profile.MCP
	if m == nil {
		return h, nil
	}
	values := mcpSecrets(raw)
	for _, v := range m.Headers {
		s, e := secretValue(values, v.Field)
		if e != nil {
			return nil, e
		}
		h.Set(v.Name, s)
	}
	a := m.Authentication
	if a == nil || a.Kind == "none" {
		return h, nil
	}
	switch a.Kind {
	case "bearer", "api-key":
		s, e := secretValue(values, "access")
		if e != nil {
			return nil, e
		}
		if a.Kind == "bearer" {
			h.Set("Authorization", "Bearer "+s)
		} else {
			h.Set(a.Header, a.Prefix+s)
		}
	case "basic":
		user, e := secretValue(values, "user")
		if e != nil {
			return nil, e
		}
		if strings.Contains(user, ":") {
			return nil, fmt.Errorf("HTTP Basic username cannot contain a colon")
		}
		pass, e := secretValue(values, "pass")
		if e != nil {
			return nil, e
		}
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
	case "oauth2":
		token, e := r.mcpOAuthToken(ctx, a, values)
		if e != nil {
			return nil, e
		}
		h.Set("Authorization", "Bearer "+token)
	}
	if value := h.Get("Authorization"); value != "" {
		r.secretMu.Lock()
		r.sensitive = append(r.sensitive, value)
		r.secretMu.Unlock()
	}
	return h, nil
}
func (r *Runtime) mcpOAuthToken(ctx context.Context, a *MCPAuthentication, values map[string]string) (string, error) {
	r.secretMu.Lock()
	defer r.secretMu.Unlock()
	keyBytes, _ := json.Marshal(values)
	key := string(keyBytes)
	if key == r.oauthRaw && time.Now().Before(r.oauthUntil) {
		return r.oauthAccess, nil
	}
	form := url.Values{"grant_type": {a.Grant}, "client_id": {a.ClientID}, "resource": {a.Resource}}
	if len(a.Scopes) > 0 {
		form.Set("scope", strings.Join(a.Scopes, " "))
	}
	if a.Grant == "refresh_token" {
		s, e := secretValue(values, "refresh")
		if e != nil {
			return "", e
		}
		form.Set("refresh_token", s)
	}
	if values["client"] != "" && a.ClientAuth != "client_secret_basic" && a.ClientAuth != "none" {
		form.Set("client_secret", values["client"])
	} else if values["client"] == "" && a.Grant == "client_credentials" && a.ClientAuth != "none" {
		return "", fmt.Errorf("OAuth client secret is missing")
	}
	req, e := http.NewRequestWithContext(ctx, "POST", a.TokenURL, strings.NewReader(form.Encode()))
	if e != nil {
		return "", fmt.Errorf("invalid OAuth token request")
	}
	if a.ClientAuth == "client_secret_basic" {
		req.SetBasicAuth(url.QueryEscape(a.ClientID), url.QueryEscape(values["client"]))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := r.client.Do(req)
	if e != nil {
		return "", fmt.Errorf("OAuth token request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("OAuth token endpoint returned HTTP %d", resp.StatusCode)
	}
	b, e := readBounded(resp.Body)
	if e != nil {
		return "", e
	}
	var result struct {
		Expires int    `json:"expires_in"`
		Access  string `json:"access_token"`
		Type    string `json:"token_type"`
		Refresh string `json:"refresh_token"`
	}
	if json.Unmarshal(b, &result) != nil || !strings.EqualFold(result.Type, "bearer") || result.Access == "" || strings.ContainsAny(result.Access, "\r\n") {
		return "", fmt.Errorf("invalid OAuth token response")
	}
	// Rotated refresh credentials must be persisted by the credential broker.
	if result.Refresh != "" && result.Refresh != values["refresh"] {
		return "", fmt.Errorf("OAuth server rotated its refresh token; use the managed OAuth connection flow")
	}
	r.sensitive = append(r.sensitive, result.Access)
	lifetime := 30 * time.Second
	if result.Expires > 0 && result.Expires < 35 {
		lifetime = time.Duration(result.Expires) * time.Second / 2
	}
	r.oauthRaw, r.oauthAccess, r.oauthUntil = key, result.Access, time.Now().Add(lifetime)
	return result.Access, nil
}
func (r *Runtime) mcpTimeout() time.Duration {
	if r.Profile.MCP != nil && r.Profile.MCP.TimeoutSeconds > 0 {
		return time.Duration(r.Profile.MCP.TimeoutSeconds) * time.Second
	}
	return 60 * time.Second
}
func (r *Runtime) redactMCP(v interface{}, raw string, keys bool) interface{} {
	v = redactValue(v, raw, keys)
	for _, s := range mcpSecrets(raw) {
		if s != "" {
			v = redactValue(v, s, keys)
		}
	}
	r.secretMu.Lock()
	defer r.secretMu.Unlock()
	for _, s := range r.sensitive {
		v = redactValue(v, s, keys)
	}
	return v
}
