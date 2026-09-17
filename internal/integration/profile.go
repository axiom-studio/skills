// Package integration implements pinned, credential-separated API and MCP operations.
package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/xeipuuv/gojsonschema"
)

const MaxBytes = 2 << 20

var identifier = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var placeholder = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9_]*)\}`)

type Credential struct {
	Binding string `json:"binding"`
	Field   string `json:"field"`
	Header  string `json:"header"`
	Prefix  string `json:"prefix"`
}
type Profile struct {
	Version    int               `json:"version"`
	ID         string            `json:"id"`
	Transport  string            `json:"transport"`
	Endpoint   string            `json:"endpoint"`
	Sources    []string          `json:"sources"`
	Credential *Credential       `json:"credential,omitempty"`
	FixedQuery map[string]string `json:"fixedQuery,omitempty"`
	Operations []Operation       `json:"operations"`
}
type Operation struct {
	Name             string                 `json:"name"`
	Description      string                 `json:"description"`
	Effect           string                 `json:"effect"`
	InputSchema      map[string]interface{} `json:"inputSchema"`
	OutputSchema     map[string]interface{} `json:"outputSchema,omitempty"`
	Method           string                 `json:"method,omitempty"`
	Path             string                 `json:"path,omitempty"`
	PathParams       map[string]string      `json:"pathParams,omitempty"`
	QueryParams      map[string]string      `json:"queryParams,omitempty"`
	BodyEncoding     string                 `json:"bodyEncoding,omitempty"`
	ResponseEncoding string                 `json:"responseEncoding,omitempty"`
	BodyParam        string                 `json:"bodyParam,omitempty"`
	Tool             string                 `json:"tool,omitempty"`
	ToolHash         string                 `json:"toolHash,omitempty"`
}

func Load(path string) (*Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil || len(raw) > MaxBytes {
		return nil, fmt.Errorf("profile exceeds size limit or cannot be read")
	}
	return decodeProfile(raw)
}
func decodeProfile(raw []byte) (*Profile, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var p Profile
	if err := d.Decode(&p); err != nil {
		return nil, fmt.Errorf("invalid profile JSON: %w", err)
	}
	if d.Decode(new(interface{})) != io.EOF {
		return nil, fmt.Errorf("trailing profile data")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}
func Digest(v interface{}) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func endpoint(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return nil, fmt.Errorf("endpoint must be HTTPS on port 443 without credentials, query, or fragment")
	}
	return u, nil
}
func (p *Profile) Validate() error {
	if p.Version != 1 || !identifier.MatchString(p.ID) || (p.Transport != "api" && p.Transport != "mcp") {
		return fmt.Errorf("invalid profile version, id, or transport")
	}
	u, err := endpoint(p.Endpoint)
	if err != nil {
		return err
	}
	if p.Transport == "api" && u.Path != "" && u.Path != "/" {
		return fmt.Errorf("API endpoint must be an origin; put paths on operations")
	}
	if len(p.Sources) == 0 {
		return fmt.Errorf("record at least one authoritative documentation source")
	}
	if p.Credential != nil {
		c := p.Credential
		// No caller-supplied headers: a profile may designate one credential header.
		h := strings.ToLower(c.Header)
		if !identifier.MatchString(c.Binding) || c.Field == "" || !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`).MatchString(c.Header) || (h != "authorization" && !strings.HasPrefix(h, "x-") && h != "api-key") || strings.ContainsAny(c.Prefix, "\r\n") {
			return fmt.Errorf("invalid credential reference/header")
		}
	}
	if len(p.Operations) > 100 {
		return fmt.Errorf("at most 100 operations per profile")
	}
	seen := map[string]bool{}
	for _, op := range p.Operations {
		if reservedAction(op.Name) || !identifier.MatchString(op.Name) || seen[op.Name] || op.Name == p.Transport+"-describe" || op.Name == p.Transport+"-execute" || op.Name == "mcp-discover" {
			return fmt.Errorf("invalid, duplicate, or reserved operation name")
		}
		seen[op.Name] = true
		if op.Description == "" || (op.Effect != "read" && op.Effect != "write") {
			return fmt.Errorf("operation needs description and explicit read/write effect")
		}
		if op.InputSchema["type"] != "object" {
			return fmt.Errorf("input schema must describe an object")
		}
		if _, err := compileSchema(op.InputSchema); err != nil {
			return fmt.Errorf("%s input: %w", op.Name, err)
		}
		if op.OutputSchema != nil {
			if _, err := compileSchema(op.OutputSchema); err != nil {
				return fmt.Errorf("%s output: %w", op.Name, err)
			}
		}
		if p.Transport == "mcp" {
			if op.Tool == "" || len(op.ToolHash) != 64 {
				return fmt.Errorf("MCP operations require tool and discovery toolHash")
			}
			if _, err := hex.DecodeString(op.ToolHash); err != nil {
				return fmt.Errorf("invalid toolHash")
			}
			if op.BodyEncoding != "" || op.ResponseEncoding != "" || op.Method != "" || op.Path != "" || len(op.PathParams)+len(op.QueryParams) > 0 || op.BodyParam != "" {
				return fmt.Errorf("MCP operation contains API fields")
			}
			continue
		}
		if op.BodyEncoding != "" && op.BodyEncoding != "json" && op.BodyEncoding != "form" && op.BodyEncoding != "text" {
			return fmt.Errorf("unsupported bodyEncoding")
		}
		if op.ResponseEncoding != "" && op.ResponseEncoding != "json" && op.ResponseEncoding != "text" {
			return fmt.Errorf("unsupported responseEncoding")
		}
		if op.Tool != "" || op.ToolHash != "" {
			return fmt.Errorf("API operation contains MCP fields")
		}
		switch op.Method {
		case "GET", "HEAD":
		case "POST", "PUT", "PATCH", "DELETE":
			if op.Effect != "write" {
				return fmt.Errorf("mutating HTTP methods require write effect")
			}
		default:
			return fmt.Errorf("unsupported HTTP method")
		}
		if !strings.HasPrefix(op.Path, "/") || strings.HasPrefix(op.Path, "//") || strings.ContainsAny(op.Path, "?#\\%") || strings.Contains(op.Path, "..") {
			return fmt.Errorf("invalid operation path")
		}
		used := map[string]bool{}
		for _, m := range placeholder.FindAllStringSubmatch(op.Path, -1) {
			if op.PathParams[m[1]] == "" {
				return fmt.Errorf("missing path parameter mapping")
			}
			used[m[1]] = true
		}
		if strings.ContainsAny(placeholder.ReplaceAllString(op.Path, ""), "{}") || len(used) != len(op.PathParams) {
			return fmt.Errorf("invalid path parameter mapping")
		}
		props, _ := op.InputSchema["properties"].(map[string]interface{})
		for _, mapping := range []map[string]string{op.PathParams, op.QueryParams} {
			for k, v := range mapping {
				if k == "" || props[v] == nil {
					return fmt.Errorf("parameter mapping references missing input property")
				}
			}
		}
		for key := range op.QueryParams {
			if _, ok := p.FixedQuery[key]; ok {
				return fmt.Errorf("query mapping cannot override fixed scope")
			}
		}
		if op.BodyParam != "" && (props[op.BodyParam] == nil || op.Method == "GET" || op.Method == "HEAD") {
			return fmt.Errorf("invalid body parameter")
		}
	}
	return nil
}

// Deliberately support only a reference-free common subset of JSON Schema.
// Never fetch $ref URLs, silently ignore newer assertion keywords, or guess defaults.
func compileSchema(schema map[string]interface{}) (*gojsonschema.Schema, error) {
	if schema == nil {
		return nil, fmt.Errorf("schema required")
	}
	if err := checkSchema(schema); err != nil {
		return nil, err
	}
	loader := gojsonschema.NewSchemaLoader()
	loader.Draft = gojsonschema.Draft7
	loader.AutoDetect = false
	loader.Validate = true
	s, err := loader.Compile(gojsonschema.NewGoLoader(schema))
	if err != nil {
		return nil, fmt.Errorf("invalid JSON Schema")
	}
	return s, nil
}
func checkSchema(s map[string]interface{}) error {
	for k, v := range s {
		switch k {
		case "$schema":
			if v != "http://json-schema.org/draft-07/schema#" && v != "https://json-schema.org/draft/2020-12/schema" {
				return fmt.Errorf("unsupported schema dialect")
			}
		case "type", "title", "description", "default", "examples", "enum", "const", "required", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties":
		case "properties", "patternProperties":
			m, ok := v.(map[string]interface{})
			if !ok {
				return fmt.Errorf("invalid schema properties")
			}
			for _, sub := range m {
				if err := checkSubschema(sub); err != nil {
					return err
				}
			}
		case "items", "additionalProperties", "not", "contains", "if", "then", "else":
			if err := checkSubschema(v); err != nil {
				return err
			}
		case "allOf", "anyOf", "oneOf":
			a, ok := v.([]interface{})
			if !ok {
				return fmt.Errorf("invalid schema combinator")
			}
			for _, sub := range a {
				if err := checkSubschema(sub); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unsupported schema keyword %q; normalize explicitly before compiling", k)
		}
	}
	return nil
}
func checkSubschema(v interface{}) error {
	if _, ok := v.(bool); ok {
		return nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid subschema")
	}
	return checkSchema(m)
}
func validateValue(schema map[string]interface{}, v interface{}) error {
	s, err := compileSchema(schema)
	if err != nil {
		return err
	}
	r, err := s.Validate(gojsonschema.NewGoLoader(v))
	if err != nil || !r.Valid() {
		return fmt.Errorf("value does not match pinned schema")
	}
	return nil
}

func reservedAction(name string) bool {
	for _, kind := range []string{"api", "mcp"} {
		for _, suffix := range []string{"compile", "read", "write", "inspect"} {
			if name == kind+"-"+suffix {
				return true
			}
		}
	}
	return name == "mcp-discover-bound"
}
