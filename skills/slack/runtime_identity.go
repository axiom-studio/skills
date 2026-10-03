package main

import (
	"bytes"
	_ "embed"
	"errors"
	"io"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

// The binary advertises the canonical identity it was built with, independently
// of image tags and environment variables supplied when it is deployed.
//
//go:embed skill.yaml
var slackSkillManifest []byte

type slackRuntimeIdentity struct {
	ID      string
	Version string
}

func slackRuntimeIdentityFromManifest(content []byte) (slackRuntimeIdentity, error) {
	var manifest struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Definition struct {
			ID      string `yaml:"id"`
			Version string `yaml:"version"`
			Source  struct {
				ResolvedVersion string `yaml:"resolvedVersion"`
			} `yaml:"source"`
		} `yaml:"definition"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&manifest); err != nil {
		return slackRuntimeIdentity{}, errors.New("canonical skill manifest cannot be decoded")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return slackRuntimeIdentity{}, errors.New("canonical skill manifest must contain one document")
	}
	if manifest.APIVersion != "openseal.dev/v1alpha1" || manifest.Kind != "SkillDefinition" {
		return slackRuntimeIdentity{}, errors.New("canonical skill manifest has an unsupported API version or kind")
	}
	if manifest.Definition.ID != slackSkillID {
		return slackRuntimeIdentity{}, errors.New("canonical skill manifest is not the Slack skill")
	}
	if _, err := semver.StrictNewVersion(manifest.Definition.Version); err != nil {
		return slackRuntimeIdentity{}, errors.New("canonical Slack skill version must be a semantic version")
	}
	if manifest.Definition.Source.ResolvedVersion != manifest.Definition.Version {
		return slackRuntimeIdentity{}, errors.New("canonical Slack skill version and source resolvedVersion must agree")
	}
	return slackRuntimeIdentity{ID: manifest.Definition.ID, Version: manifest.Definition.Version}, nil
}
