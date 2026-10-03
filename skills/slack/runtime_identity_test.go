package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	skillpb "github.com/axiom-studio/skills.sdk/grpc/skillpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gopkg.in/yaml.v3"
)

func TestSlackRuntimeIdentityRejectsInvalidCanonicalMetadata(t *testing.T) {
	valid := "apiVersion: openseal.dev/v1alpha1\nkind: SkillDefinition\ndefinition:\n  id: skill-slack\n  version: 9.8.7\n  source:\n    resolvedVersion: 9.8.7\n"
	if identity, err := slackRuntimeIdentityFromManifest([]byte(valid)); err != nil || identity.ID != slackSkillID || identity.Version != "9.8.7" {
		t.Fatalf("valid identity = %#v, %v", identity, err)
	}
	for name, content := range map[string]string{
		"empty":             "",
		"malformed":         "definition: [",
		"wrong API":         strings.Replace(valid, "openseal.dev/v1alpha1", "unsupported", 1),
		"wrong kind":        strings.Replace(valid, "SkillDefinition", "AgentDefinition", 1),
		"wrong skill":       strings.Replace(valid, "skill-slack", "skill-other", 1),
		"missing version":   strings.Replace(valid, "  version: 9.8.7\n", "", 1),
		"invalid version":   strings.ReplaceAll(valid, "9.8.7", "v9.8"),
		"missing source":    strings.Replace(valid, "  source:\n    resolvedVersion: 9.8.7\n", "", 1),
		"source mismatch":   strings.Replace(valid, "resolvedVersion: 9.8.7", "resolvedVersion: 9.8.6", 1),
		"duplicate version": valid + "  version: 9.8.6\n",
		"extra document":    valid + "---\nkind: SkillDefinition\n",
	} {
		t.Run(name, func(t *testing.T) {
			if identity, err := slackRuntimeIdentityFromManifest([]byte(content)); err == nil {
				t.Fatalf("accepted invalid identity: %#v", identity)
			}
		})
	}
}

func TestSlackRuntimeHealthAndIngressMatchEmbeddedManifest(t *testing.T) {
	content, err := os.ReadFile("skill.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, slackSkillManifest) {
		t.Fatal("runtime manifest differs from the canonical source manifest")
	}
	var manifest struct {
		Definition struct {
			ID                   string                 `yaml:"id"`
			Version              string                 `yaml:"version"`
			Actions              map[string]interface{} `yaml:"actions"`
			ConversationAdapters map[string]struct {
				Transport struct {
					IngressEndpoint  string `yaml:"ingressEndpoint"`
					DeliveryEndpoint string `yaml:"deliveryEndpoint"`
				} `yaml:"transport"`
			} `yaml:"conversationAdapters"`
			CallbackAdapters map[string]struct {
				Transport struct {
					IngressEndpoint string `yaml:"ingressEndpoint"`
				} `yaml:"transport"`
			} `yaml:"callbackAdapters"`
		} `yaml:"definition"`
	}
	if err := yaml.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	server, err := newSlackSkillServer()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	transport := grpc.NewServer()
	skillpb.RegisterSkillServiceServer(transport, server)
	t.Cleanup(transport.Stop)
	go func() { _ = transport.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///"+listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := skillpb.NewSkillServiceClient(connection)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	health, err := client.Health(ctx, &skillpb.HealthRequest{})
	if err != nil || !health.GetHealthy() || health.GetSkillId() != manifest.Definition.ID || health.GetVersion() != manifest.Definition.Version {
		t.Fatalf("Health does not match canonical manifest: response=%v error=%v", health, err)
	}
	nodes, err := client.GetNodeTypes(ctx, &skillpb.GetNodeTypesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	registered := make(map[string]bool)
	for _, node := range nodes.GetNodeTypes() {
		registered[node] = true
	}
	for action := range manifest.Definition.Actions {
		if !registered[action] {
			t.Errorf("declared action %q is not registered", action)
		}
	}
	for name, adapter := range manifest.Definition.ConversationAdapters {
		if !registered[adapter.Transport.IngressEndpoint] || !registered[adapter.Transport.DeliveryEndpoint] {
			t.Errorf("declared conversation adapter %q is not registered", name)
		}
	}
	for name, adapter := range manifest.Definition.CallbackAdapters {
		if !registered[adapter.Transport.IngressEndpoint] {
			t.Errorf("declared callback adapter %q is not registered", name)
		}
	}

	now := time.Now().UTC()
	body := []byte(fmt.Sprintf(`{"type":"event_callback","team_id":"T123","api_app_id":"A123","event_id":"Ev123","event_time":%d,"authorizations":[{"user_id":"U-BOT","team_id":"T123","is_bot":true}],"event":{"type":"app_mention","user":"U123","text":"<@U-BOT> hello","channel":"C123","channel_type":"channel","ts":"1720000000.123456"}}`, now.Unix()))
	config := ingressConfig(now, body, &conversationEndpoint{
		ID: "endpoint", Provider: "slack", Address: "C123",
		Configuration: map[string]interface{}{"teamId": "T123", "appId": "A123"},
	})
	encodedConfig := make(map[string][]byte)
	for key, value := range config {
		encodedConfig[key], err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := client.Execute(ctx, &skillpb.ExecuteRequest{
		NodeType: slackIngressNodeType, Config: encodedConfig,
		Bindings: map[string][]byte{slackSigningSecretKey: []byte(`"signing-secret"`)},
	})
	if err != nil || result.GetError() != nil {
		t.Fatalf("registered ingress execution failed: result=%v error=%v", result, err)
	}
	var events []normalizedConversationEvent
	if err := json.Unmarshal(result.GetOutput()["events"], &events); err != nil || len(events) != 1 || events[0].Text != "hello" || events[0].ExternalConversationID != "C123" {
		t.Fatalf("registered ingress did not normalize the signed message: events=%#v error=%v", events, err)
	}
}
