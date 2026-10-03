package main

import (
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v3"
)

func TestTelegramConversationAdapterManifestContract(t *testing.T) {
	data, err := os.ReadFile("skill.yaml")
	if err != nil {
		t.Fatalf("read Skill manifest: %v", err)
	}
	var manifest struct {
		Definition struct {
			Version    string `yaml:"version"`
			Installers []struct {
				Package string `yaml:"package"`
			} `yaml:"installers"`
			Source struct {
				ResolvedVersion string `yaml:"resolvedVersion"`
			} `yaml:"source"`
			ConversationAdapters map[string]struct {
				ProtocolVersion   string   `yaml:"protocolVersion"`
				Provider          string   `yaml:"provider"`
				EndpointModes     []string `yaml:"endpointModes"`
				InboundEventTypes []string `yaml:"inboundEventTypes"`
				SubjectEvidence   []struct {
					Action      string `yaml:"action"`
					SubjectPath string `yaml:"subjectPath"`
				} `yaml:"subjectEvidence"`
				Credentials []struct {
					Name string `yaml:"name"`
					Kind string `yaml:"kind"`
				} `yaml:"credentials"`
				Delivery struct {
					Operations  []string `yaml:"operations"`
					Ordering    string   `yaml:"ordering"`
					Idempotency string   `yaml:"idempotency"`
				} `yaml:"delivery"`
				Transport struct {
					Kind                string   `yaml:"kind"`
					IngressEndpoint     string   `yaml:"ingressEndpoint"`
					DeliveryEndpoint    string   `yaml:"deliveryEndpoint"`
					IngressCredentials  []string `yaml:"ingressCredentials"`
					DeliveryCredentials []string `yaml:"deliveryCredentials"`
				} `yaml:"transport"`
			} `yaml:"conversationAdapters"`
		} `yaml:"definition"`
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode Skill manifest: %v", err)
	}
	adapter, ok := manifest.Definition.ConversationAdapters["conversations"]
	if !ok {
		t.Fatal("conversations adapter is missing")
	}
	if adapter.ProtocolVersion != "openseal.conversation.adapter/v1" || adapter.Provider != "telegram" ||
		!reflect.DeepEqual(adapter.EndpointModes, []string{"channel", "direct"}) ||
		!reflect.DeepEqual(adapter.InboundEventTypes, []string{"conversation.message.received"}) {
		t.Fatalf("conversation identity = %#v", adapter)
	}
	if len(adapter.Credentials) != 1 || adapter.Credentials[0].Name != telegramCredentialKey || adapter.Credentials[0].Kind != telegramCredentialKey ||
		!reflect.DeepEqual(adapter.Transport.IngressCredentials, []string{telegramCredentialKey}) ||
		!reflect.DeepEqual(adapter.Transport.DeliveryCredentials, []string{telegramCredentialKey}) {
		t.Fatalf("conversation credentials = %#v, transport = %#v", adapter.Credentials, adapter.Transport)
	}
	if adapter.Transport.Kind != "plugin" || adapter.Transport.IngressEndpoint != telegramIngressNodeType || adapter.Transport.DeliveryEndpoint != telegramDeliveryNodeType ||
		!reflect.DeepEqual(adapter.Delivery.Operations, []string{"message.send", "message.update", "typing.set"}) ||
		adapter.Delivery.Ordering != "thread" || adapter.Delivery.Idempotency != "supported" {
		t.Fatalf("conversation delivery = %#v, transport = %#v", adapter.Delivery, adapter.Transport)
	}
	if manifest.Definition.Version != telegramSkillVersion || manifest.Definition.Source.ResolvedVersion != telegramSkillVersion ||
		len(manifest.Definition.Installers) != 1 || manifest.Definition.Installers[0].Package != "axiomstudio/skill-telegram:latest" {
		t.Fatalf("version contract = %#v", manifest.Definition)
	}
	// The SDK rejects manifests whose adapter lists differ from canonical order.
	// Keep subject evidence sorted by action, as NormalizeConversationAdapter does.
	var evidenceActions []string
	for _, evidence := range adapter.SubjectEvidence {
		if evidence.SubjectPath != "chatId" {
			t.Fatalf("subject evidence must identify the Telegram chat: %#v", evidence)
		}
		evidenceActions = append(evidenceActions, evidence.Action)
	}
	if !sort.StringsAreSorted(evidenceActions) || !reflect.DeepEqual(evidenceActions, []string{
		"telegram-get-chat", "telegram-send-document", "telegram-send-message", "telegram-send-photo",
	}) {
		t.Fatalf("subject evidence must use SDK canonical action ordering: %v", evidenceActions)
	}
}

// Exercise real executor receipts against the published schemas. These fields
// establish connection identity and destination evidence in the host.
func requireTelegramActionOutputSchema(t *testing.T, action string, output interface{}) {
	t.Helper()
	data, err := os.ReadFile("skill.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Definition struct {
			Actions map[string]struct {
				OutputSchema map[string]interface{} `yaml:"outputSchema"`
			} `yaml:"actions"`
		} `yaml:"definition"`
	}
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	schema := manifest.Definition.Actions[action].OutputSchema
	if schema["type"] != "object" {
		t.Fatalf("%s must declare its output object schema", action)
	}
	result, err := gojsonschema.Validate(gojsonschema.NewGoLoader(schema), gojsonschema.NewGoLoader(output))
	if err != nil {
		t.Fatalf("validate %s output schema: %v", action, err)
	}
	if !result.Valid() {
		t.Fatalf("%s receipt violates its published output schema: %v", action, result.Errors())
	}
}
