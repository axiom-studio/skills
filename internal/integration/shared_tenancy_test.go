package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	goSharedIsolationTest     = regexp.MustCompile(`(?m)^func Test\w*Shared\w*\(t \*testing\.T\)`)
	pythonSharedIsolationTest = regexp.MustCompile(`(?m)^class SharedRuntimeIsolationTest\(`)
)

// A Skill declaring `tenancy: shared` is hosted once for every tenant. The host
// only shares an exact, version-pinned official executable that carries no
// tenant state, and each such Skill proves request isolation in its own tests.
func TestSharedTenancySkillsAreRequestLocal(t *testing.T) {
	paths, err := filepath.Glob("../../skills/*/skill.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("find published manifests: %v", err)
	}
	shared := 0
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Definition struct {
				ID           string `yaml:"id"`
				Version      string `yaml:"version"`
				Requirements struct {
					Tenancy       string        `yaml:"tenancy"`
					Environment   []interface{} `yaml:"environment"`
					Configuration []interface{} `yaml:"configuration"`
					Storage       []interface{} `yaml:"storage"`
				} `yaml:"requirements"`
				Installers []struct {
					Kind    string `yaml:"kind"`
					Package string `yaml:"package"`
				} `yaml:"installers"`
				Source struct {
					ResolvedVersion string `yaml:"resolvedVersion"`
				} `yaml:"source"`
			} `yaml:"definition"`
		}
		if err := yaml.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		definition := manifest.Definition
		switch definition.Requirements.Tenancy {
		case "", "tenant":
			continue
		case "shared":
		default:
			t.Errorf("%s: tenancy %q is invalid", path, definition.Requirements.Tenancy)
			continue
		}
		shared++
		t.Run(definition.ID, func(t *testing.T) {
			if len(definition.Requirements.Environment) != 0 || len(definition.Requirements.Configuration) != 0 || len(definition.Requirements.Storage) != 0 {
				t.Error("a shared Skill cannot declare environment, configuration or storage requirements")
			}
			if len(definition.Installers) != 1 || definition.Installers[0].Kind != "oci" ||
				definition.Installers[0].Package != "axiomstudio/"+definition.ID+":"+definition.Version {
				t.Errorf("a shared Skill needs one OCI installer pinned to axiomstudio/%s:%s, got %+v", definition.ID, definition.Version, definition.Installers)
			}
			if definition.Source.ResolvedVersion != definition.Version {
				t.Errorf("source resolvedVersion %q differs from version %q", definition.Source.ResolvedVersion, definition.Version)
			}
			if !hasSharedIsolationTest(t, filepath.Dir(path)) {
				t.Error("a shared Skill needs a TestShared* Go test or a SharedRuntimeIsolationTest Python test proving request isolation")
			}
		})
	}
	if shared == 0 {
		t.Fatal("no Skill declares shared tenancy")
	}
}

func hasSharedIsolationTest(t *testing.T, directory string) bool {
	t.Helper()
	for pattern, test := range map[string]*regexp.Regexp{"*_test.go": goSharedIsolationTest, "test_*.py": pythonSharedIsolationTest} {
		files, err := filepath.Glob(filepath.Join(directory, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if test.Match(raw) {
				return true
			}
		}
	}
	return false
}
