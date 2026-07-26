package main

import (
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

type manifestCredential struct {
	Name   string `yaml:"name"`
	Kind   string `yaml:"kind"`
	OAuth2 struct {
		Provider string   `yaml:"provider"`
		Subject  string   `yaml:"subject"`
		Scopes   []string `yaml:"scopes"`
	} `yaml:"oauth2"`
}

func TestDestinationDiscoveryCredentialMatchesAdapterContract(t *testing.T) {
	data, err := os.ReadFile("skill.yaml")
	if err != nil {
		t.Fatalf("read Skill manifest: %v", err)
	}

	var manifest struct {
		Definition struct {
			Actions map[string]struct {
				Credentials []manifestCredential `yaml:"credentials"`
			} `yaml:"actions"`
			ConversationAdapters map[string]struct {
				Credentials []manifestCredential `yaml:"credentials"`
				Discovery   []struct {
					Action string `yaml:"action"`
				} `yaml:"destinationDiscovery"`
			} `yaml:"conversationAdapters"`
		} `yaml:"definition"`
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode Skill manifest: %v", err)
	}

	for adapterName, adapter := range manifest.Definition.ConversationAdapters {
		for _, discovery := range adapter.Discovery {
			action, ok := manifest.Definition.Actions[discovery.Action]
			if !ok {
				t.Fatalf("adapter %s references missing discovery action %s", adapterName, discovery.Action)
			}
			for _, actionCredential := range action.Credentials {
				matched := false
				for _, adapterCredential := range adapter.Credentials {
					if actionCredential.Name == adapterCredential.Name && reflect.DeepEqual(actionCredential, adapterCredential) {
						matched = true
						break
					}
				}
				if !matched {
					t.Fatalf("adapter %s discovery action %s credential %s does not match the adapter contract", adapterName, discovery.Action, actionCredential.Name)
				}
			}
		}
	}
}
