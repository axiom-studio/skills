package integration

import (
	"net/http"
	"testing"
)

func TestMCPHeaderEncodingAndNestedSchema(t *testing.T) {
	schema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"nested": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"region": map[string]interface{}{"type": "string", "x-mcp-header": "Region"}}}}}
	h := http.Header{}
	if err := mirrorMCPHeaders(schema, map[string]interface{}{"nested": map[string]interface{}{"region": "Hello, 世界"}}, h); err != nil {
		t.Fatal(err)
	}
	if got := h.Get("Mcp-Param-Region"); got != "=?base64?SGVsbG8sIOS4lueVjA==?=" {
		t.Fatal(got)
	}
	for _, raw := range []string{" padded ", "bad\r\nvalue", "=?base64?a?="} {
		if mcpHeaderValue(raw) == raw {
			t.Fatalf("unsafe value %q was not encoded", raw)
		}
	}
	bad := map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string", "x-mcp-header": "Region"}}
	if validateMCPHeaderSchema(bad) == nil {
		t.Fatal("array annotation accepted")
	}
}
