package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Check the actual published definitions, including skills with hand-authored
// manifests, so a new skill cannot silently reintroduce an immutable image tag.
func TestPublishedSkillImagesUseLatest(t *testing.T) {
	paths, err := filepath.Glob("../../skills/*/skill.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("find published manifests: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var manifest struct {
				Definition struct {
					Version    string `yaml:"version"`
					Installers []struct {
						Kind    string `yaml:"kind"`
						Package string `yaml:"package"`
					} `yaml:"installers"`
				} `yaml:"definition"`
			}
			if err := yaml.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.Definition.Version == "latest" {
				t.Fatal("contract version must remain explicit")
			}
			for _, installer := range manifest.Definition.Installers {
				if installer.Kind == "oci" && !strings.HasSuffix(installer.Package, ":latest") {
					t.Errorf("OCI installer %q must use :latest", installer.Package)
				}
			}
		})
	}
}

func TestGeneratedSkillImagesUseLatest(t *testing.T) {
	for _, transport := range []string{"api", "mcp"} {
		manifest, err := BaseManifest(transport, "Skill instructions")
		if err != nil {
			t.Fatal(err)
		}
		definition := manifest["definition"].(map[string]interface{})
		installer := definition["installers"].([]interface{})[0].(map[string]interface{})
		if installer["package"] != "axiomstudio/skill-"+transport+":latest" {
			t.Fatalf("%s generated installer: %v", transport, installer)
		}
	}
	compiled, err := Compile(fixture())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled["manifest"].(string), "package: axiomstudio/skill-api:latest") {
		t.Fatal("compiled integration installer must use :latest")
	}
}
