package veiledbrowser_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestManifestMatchesNativeJavaScriptRuntime(t *testing.T) {
	encoded, err := os.ReadFile("skill.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]interface{}
	if err := yaml.Unmarshal(encoded, &manifest); err != nil {
		t.Fatalf("manifest is invalid YAML: %v", err)
	}
	text := string(encoded)
	for _, expected := range []string{
		"id: skill-veiled-browser", "version: 1.0.0", "package: axiomstudio/skill-veiled-browser:1.0.0",
		"externalOperationPolicy: required", "durability: persistent", "resolvedVersion: 1.0.0",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("manifest is missing %q", expected)
		}
	}
	for _, forbidden := range []string{"javascript", "rawCdp", "cookieJar", "proxyUrl", "launchArgs"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("manifest exposes forbidden control %q", forbidden)
		}
	}
	lock, err := os.ReadFile("package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, pinned := range []string{`"@achamm/veilbrowser": "1.3.1"`, `"@grpc/grpc-js": "1.14.4"`} {
		if !strings.Contains(string(lock), pinned) {
			t.Fatalf("lockfile does not pin %s", pinned)
		}
	}
}
