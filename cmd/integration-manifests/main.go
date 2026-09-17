// Regenerate canonical manifests from the shared contract and learning guides.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/axiom-studio/skills/internal/integration"
	"gopkg.in/yaml.v3"
)

func main() {
	reference, err := os.ReadFile("internal/integration/README.md")
	if err != nil {
		panic(err)
	}
	for _, kind := range []string{"api", "mcp"} {
		root := "skills/" + kind + "/"
		markdown, err := os.ReadFile(root + "SKILL.md")
		if err != nil {
			panic(err)
		}
		parts := strings.SplitN(string(markdown), "---", 3)
		if len(parts) != 3 {
			panic("missing skill frontmatter")
		}
		instructions := strings.ReplaceAll(strings.TrimSpace(parts[2]), "[contract reference](../../internal/integration/README.md)", "contract reference below") + "\n\n" + string(reference)
		manifest, err := integration.BaseManifest(kind, instructions)
		if err != nil {
			panic(err)
		}
		data, err := yaml.Marshal(manifest)
		if err != nil {
			panic(err)
		}
		if err = os.WriteFile(root+"skill.yaml", data, 0644); err != nil {
			panic(err)
		}
		fmt.Println("Generated", root+"skill.yaml")
	}
}
