package integration

import (
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"strings"
)

func mcpHeaderValue(value string) string {
	safe := strings.TrimSpace(value) == value && !(strings.HasPrefix(value, "=?base64?") && strings.HasSuffix(value, "?="))
	for _, c := range value {
		if (c < 32 && c != '\t') || c > 126 {
			safe = false
		}
	}
	if safe {
		return value
	}
	return "=?base64?" + base64.StdEncoding.EncodeToString([]byte(value)) + "?="
}

// Only annotations reached through properties may become protocol headers.
func walkMCPHeaders(schema map[string]interface{}, path []string, allowed bool, seen map[string]bool, visit func(string, []string, string) error) error {
	if annotation, exists := schema["x-mcp-header"]; exists {
		name, ok := annotation.(string)
		typ, _ := schema["type"].(string)
		if !ok || !allowed || len(path) == 0 || !headerName.MatchString(name) || seen[strings.ToLower(name)] || (typ != "string" && typ != "integer" && typ != "boolean") {
			return fmt.Errorf("invalid MCP parameter header annotation")
		}
		seen[strings.ToLower(name)] = true
		if err := visit(name, path, typ); err != nil {
			return err
		}
	}
	for key, value := range schema {
		if key == "properties" {
			properties, ok := value.(map[string]interface{})
			if !ok {
				return fmt.Errorf("invalid MCP schema properties")
			}
			for name, sub := range properties {
				if child, ok := sub.(map[string]interface{}); ok {
					next := append(append([]string(nil), path...), name)
					if err := walkMCPHeaders(child, next, allowed, seen, visit); err != nil {
						return err
					}
				}
			}
		} else if key == "items" || key == "additionalProperties" || key == "patternProperties" || key == "allOf" || key == "anyOf" || key == "oneOf" || key == "not" || key == "if" || key == "then" || key == "else" || key == "$defs" || key == "definitions" || key == "contains" || key == "prefixItems" {
			if err := rejectMCPAnnotations(value); err != nil {
				return err
			}
		}
	}
	return nil
}
func rejectMCPAnnotations(v interface{}) error {
	switch x := v.(type) {
	case map[string]interface{}:
		if _, ok := x["x-mcp-header"]; ok {
			return fmt.Errorf("MCP header annotation is not reachable through properties")
		}
		for _, child := range x {
			if err := rejectMCPAnnotations(child); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, child := range x {
			if err := rejectMCPAnnotations(child); err != nil {
				return err
			}
		}
	}
	return nil
}
func validateMCPHeaderSchema(schema map[string]interface{}) error {
	return walkMCPHeaders(schema, nil, true, map[string]bool{}, func(string, []string, string) error { return nil })
}
func mirrorMCPHeaders(schema map[string]interface{}, args map[string]interface{}, headers http.Header) error {
	return walkMCPHeaders(schema, nil, true, map[string]bool{}, func(name string, path []string, typ string) error {
		var value interface{} = args
		for _, key := range path {
			object, ok := value.(map[string]interface{})
			if !ok {
				return nil
			}
			value, ok = object[key]
			if !ok {
				return nil
			}
		}
		var text string
		switch typ {
		case "string":
			s, ok := value.(string)
			if !ok {
				return fmt.Errorf("invalid MCP header argument")
			}
			text = s
		case "boolean":
			b, ok := value.(bool)
			if !ok {
				return fmt.Errorf("invalid MCP header argument")
			}
			text = fmt.Sprint(b)
		case "integer":
			n, ok := value.(float64)
			if !ok || math.Trunc(n) != n || math.Abs(n) > 9007199254740991 {
				return fmt.Errorf("MCP header integer exceeds safe range")
			}
			text = fmt.Sprintf("%.0f", n)
		}
		headers.Set("Mcp-Param-"+name, mcpHeaderValue(text))
		return nil
	})
}
