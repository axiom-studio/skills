package main

import (
	"os"
	"reflect"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCallbackEventTypesUseCanonicalOrdering(t *testing.T) {
	data, err := os.ReadFile("skill.yaml")
	if err != nil {
		t.Fatalf("read Skill manifest: %v", err)
	}
	var manifest struct {
		Definition struct {
			CallbackAdapters map[string]struct {
				EventTypes []string `yaml:"eventTypes"`
			} `yaml:"callbackAdapters"`
		} `yaml:"definition"`
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode Skill manifest: %v", err)
	}
	events := manifest.Definition.CallbackAdapters["repository-events"].EventTypes
	canonical := append([]string(nil), events...)
	sort.Strings(canonical)
	if !reflect.DeepEqual(events, canonical) {
		t.Fatalf("callback event types are not canonical: %v", events)
	}
}
