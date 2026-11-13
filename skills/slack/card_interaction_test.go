package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// cardVisualJSON is a visual with every component Slack lets people answer.
const cardVisualJSON = `{"artifactId":"visual-9","version":3,"fallbackText":"Dinner","surface":{"title":"Dinner","components":[` +
	`{"id":"pick","type":"choices","summary":"Where","choices":{"chips":[{"id":"thai","label":"Thai"},{"id":"pizza","label":"Pizza"}]}},` +
	`{"id":"many","type":"choices","summary":"Many","choices":{"chips":[{"id":"a","label":"A"},{"id":"b","label":"B"},{"id":"c","label":"C"},{"id":"d","label":"D"},{"id":"e","label":"E"},{"id":"f","label":"F"}]}},` +
	`{"id":"next","type":"actions","actions":[{"id":"go","label":"Go ahead","kind":"respond"}]},` +
	`{"id":"approve","type":"actions","actions":[{"id":"ok","label":"Approve","kind":"approval","decision":"approve"}]},` +
	`{"id":"check","type":"quiz","quiz":{"question":"Spicy?","options":[{"id":"t","label":"Thai"},{"id":"p","label":"Pizza"}]}},` +
	`{"id":"vote","type":"poll","poll":{"question":"When?","multiple":true,"options":[{"id":"six","label":"Six"},{"id":"eight","label":"Eight"}]}},` +
	`{"id":"closed","type":"poll","poll":{"question":"Where?","closesAt":"2020-01-01T00:00:00Z","options":[{"id":"x","label":"X"}]}},` +
	`{"id":"rsvp","type":"form","title":"RSVP","form":{"fields":[{"id":"name","type":"text","label":"Name","required":true},` +
	`{"id":"guests","type":"number","label":"Guests","min":0},{"id":"diet","type":"select","label":"Diet","options":[{"label":"Vegan","value":"vegan"}]},` +
	`{"id":"agree","type":"checkbox","label":"I agree"}]}}]},` +
	`"answers":[{"componentId":"pick","people":2,"chosen":[{"id":"thai","count":2}]},{"componentId":"rsvp","people":1}]}`

func renderedCard(t *testing.T) []slackBlock {
	t.Helper()
	blocks, _ := slackVisualMessage("", []conversationVisual{decodeTestVisual(t, cardVisualJSON)}, "endpoint-7")
	return blocks
}

// actionsOf returns the elements of the actions block that follows the
// section whose text starts with lead.
func actionsOf(t *testing.T, blocks []slackBlock, lead string) []slackBlock {
	t.Helper()
	for index, block := range blocks {
		text, _ := block["text"].(slackBlock)
		if content, _ := text["text"].(string); block["type"] == "section" && strings.HasPrefix(content, lead) {
			if index+1 < len(blocks) && blocks[index+1]["type"] == "actions" {
				return blocks[index+1]["elements"].([]slackBlock)
			}
			return nil
		}
	}
	t.Fatalf("no section %q in %s", lead, blocksJSON(t, blocks))
	return nil
}

func cardRefOf(t *testing.T, element slackBlock) slackCardRef {
	t.Helper()
	var ref slackCardRef
	if err := json.Unmarshal([]byte(element["value"].(string)), &ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestCardChoicesDrawAsButtonsThatMarkWhatWasChosen(t *testing.T) {
	blocks := renderedCard(t)
	buttons := actionsOf(t, blocks, "Where")
	if len(buttons) != 2 || buttons[0]["type"] != "button" || buttons[0]["style"] != "primary" || buttons[1]["style"] != nil {
		t.Fatalf("buttons = %#v", buttons)
	}
	ref := cardRefOf(t, buttons[0])
	if ref != (slackCardRef{Destination: "endpoint-7", ArtifactID: "visual-9", Version: 3, ComponentID: "pick", Kind: cardChip, Element: "thai"}) {
		t.Fatalf("ref = %#v", ref)
	}
	if !strings.Contains(blocksJSON(t, blocks), "Thai 2 — picked by 2 people") {
		t.Fatalf("answers not shown: %s", blocksJSON(t, blocks))
	}
	ids := map[string]bool{}
	for _, block := range blocks {
		elements, _ := block["elements"].([]slackBlock)
		for _, element := range elements {
			if id, ok := element["action_id"].(string); ok {
				if ids[id] || !isSlackCardAction(id) || len(id) > 255 {
					t.Fatalf("action id %q is not a unique card action", id)
				}
				ids[id] = true
			}
		}
	}
}

func TestCardWithManyChoicesDrawsASelectCarryingItsReferenceInTheBlockID(t *testing.T) {
	blocks := renderedCard(t)
	var menu slackBlock
	for _, block := range blocks {
		if block["type"] == "actions" && block["block_id"] != nil {
			menu = block
		}
	}
	if menu == nil {
		t.Fatalf("no select in %s", blocksJSON(t, blocks))
	}
	element := menu["elements"].([]slackBlock)[0]
	options := element["options"].([]slackBlock)
	ref, ok := decodeSlackCardRef("", menu["block_id"].(string), options[5]["value"].(string))
	if element["type"] != "static_select" || len(options) != 6 || !ok || ref.ComponentID != "many" || ref.Element != "f" || ref.Kind != cardChip {
		t.Fatalf("select = %#v, ref = %#v", menu, ref)
	}
}

func TestCardDrawsOnlyRespondActionsQuizzesOpenPollsAndForms(t *testing.T) {
	blocks := renderedCard(t)
	if refs := actionsOf(t, blocks, "*Spicy?*"); len(refs) != 2 || cardRefOf(t, refs[0]).Kind != cardQuiz {
		t.Fatalf("quiz = %#v", refs)
	}
	vote := actionsOf(t, blocks, "*When?*")
	if len(vote) != 1 || cardRefOf(t, vote[0]).Kind != cardPollMany || cardRefOf(t, vote[0]).Modal.Fields[0].Type != "many" {
		t.Fatalf("multiple-choice poll = %#v", vote)
	}
	if closed := actionsOf(t, blocks, "*Where?*"); closed != nil {
		t.Fatalf("a closed poll took votes: %#v", closed)
	}
	encoded := blocksJSON(t, blocks)
	if strings.Contains(encoded, `\"c\":\"approve\"`) || !strings.Contains(encoded, `\"c\":\"next\"`) {
		t.Fatalf("actions = %s", encoded)
	}
	form := actionsOf(t, blocks, "*RSVP*")
	modal := cardRefOf(t, form[0]).Modal
	if len(form) != 1 || modal == nil || len(modal.Fields) != 4 || modal.Fields[1].Type != "number" || modal.Fields[2].Options[0] != [2]string{"Vegan", "vegan"} {
		t.Fatalf("form = %#v", form)
	}
	if !strings.Contains(encoded, "Sent by 1 person") {
		t.Fatalf("form answers not shown: %s", encoded)
	}
}

func TestCardWithoutDestinationDrawsAsText(t *testing.T) {
	blocks, _ := slackVisualMessage("", []conversationVisual{decodeTestVisual(t, cardVisualJSON)}, "")
	for _, block := range blocks {
		if block["type"] == "actions" {
			t.Fatalf("a card without a destination took answers: %s", blocksJSON(t, blocks))
		}
	}
}

func TestSocketModeRoutesCardAnswersToTheirEndpoint(t *testing.T) {
	routes := map[string]string{"endpoint-7": "http://sentinel/policy", "conversation_endpoint:endpoint-7": "http://sentinel/conversation"}
	button := renderedCardButton(t, "Where")
	payload, _ := json.Marshal(map[string]interface{}{"type": "block_actions", "actions": []slackBlock{{"action_id": button["action_id"], "value": button["value"]}}})
	if route, err := slackSocketCallbackURL(payload, routes); err != nil || route != routes["conversation_endpoint:endpoint-7"] {
		t.Fatalf("button route %q %v", route, err)
	}
	blockID, _ := json.Marshal(slackCardRef{Destination: "endpoint-7", ArtifactID: "visual-9", Version: 3, ComponentID: "many", Kind: cardChip})
	selected, _ := json.Marshal(map[string]interface{}{"type": "block_actions", "actions": []slackBlock{{"action_id": "openseal_card:3", "block_id": string(blockID), "selected_option": slackBlock{"value": "f"}}}})
	if route, err := slackSocketCallbackURL(selected, routes); err != nil || route != routes["conversation_endpoint:endpoint-7"] {
		t.Fatalf("select route %q %v", route, err)
	}
	metadata, _ := json.Marshal(slackCardViewMetadata{Ref: slackCardRef{Destination: "endpoint-7", ArtifactID: "visual-9", Version: 3, ComponentID: "rsvp", Kind: cardForm}, ChannelID: "C123", MessageID: "1.1"})
	submitted, _ := json.Marshal(map[string]interface{}{"type": "view_submission", "view": slackBlock{"callback_id": slackCardFormCallback, "private_metadata": string(metadata)}})
	if route, err := slackSocketCallbackURL(submitted, routes); err != nil || route != routes["conversation_endpoint:endpoint-7"] {
		t.Fatalf("form route %q %v", route, err)
	}
	delete(routes, "conversation_endpoint:endpoint-7")
	if _, err := slackSocketCallbackURL(payload, routes); err == nil {
		t.Fatal("a card answer borrowed the policy callback")
	}
}

func renderedCardButton(t *testing.T, lead string) slackBlock {
	t.Helper()
	return actionsOf(t, renderedCard(t), lead)[0]
}

// cardIngress sends a signed interaction payload to the endpoint's ingress.
func cardIngress(t *testing.T, adapter *slackAdapter, payload interface{}) map[string]interface{} {
	t.Helper()
	encoded, _ := json.Marshal(payload)
	body := []byte(url.Values{"payload": []string{string(encoded)}}.Encode())
	now := time.Now().UTC()
	config := ingressConfig(now, body, &conversationEndpoint{ID: "endpoint-7", Provider: "slack", Address: "C123", Configuration: map[string]interface{}{"teamId": "T123", "appId": "A123"}})
	config[slackConnectionKey] = "xoxb-bot"
	request := config[adapterEnvelopeKey].(map[string]interface{})["request"].(*conversationIngressRequest)
	request.Headers["Content-Type"] = []string{"application/x-www-form-urlencoded"}
	output, err := adapter.ingress(context.Background(), config)
	if err != nil || output["statusCode"] != http.StatusOK {
		t.Fatalf("ingress = %#v, %v", output, err)
	}
	return output
}

func cardEvents(t *testing.T, output map[string]interface{}) []normalizedConversationEvent {
	t.Helper()
	encoded, _ := json.Marshal(output["events"])
	var events []normalizedConversationEvent
	if err := json.Unmarshal(encoded, &events); err != nil {
		t.Fatal(err)
	}
	return events
}

func clickPayload(action slackBlock) map[string]interface{} {
	action["action_ts"] = "1720000000.5"
	return map[string]interface{}{
		"type": "block_actions", "api_app_id": "A123", "trigger_id": "trigger-1",
		"team": map[string]string{"id": "T123"}, "user": map[string]string{"id": "U9", "username": "kev", "name": "Kev"},
		"channel": map[string]string{"id": "C123"}, "container": map[string]string{"message_ts": "1720000000.1", "thread_ts": "1720000000.0"},
		"actions": []slackBlock{action},
	}
}

func TestCardButtonClickBecomesACardAnswerEvent(t *testing.T) {
	adapter := newSlackAdapter("signing-secret", "", nil)
	button := renderedCardButton(t, "Where")

	events := cardEvents(t, cardIngress(t, adapter, clickPayload(slackBlock{"action_id": button["action_id"], "value": button["value"]})))

	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	event := events[0]
	encoded, _ := json.Marshal(event.Attributes)
	if event.Type != "conversation.card.interacted" || event.ExternalMessageID != "1720000000.1" || event.ExternalThreadID != "1720000000.0" ||
		event.ExternalParticipantID != "U9" || event.ParticipantDisplayName != "Kev" || event.ExternalConversationID != "C123" || event.Direct ||
		string(encoded) != `{"artifactId":"visual-9","componentId":"pick","teamId":"T123","values":{"chipId":"thai"},"version":3}` {
		t.Fatalf("event = %#v, attributes %s", event, encoded)
	}
}

func TestCardSelectAndPollClicksCarryTheirAnswer(t *testing.T) {
	adapter := newSlackAdapter("signing-secret", "", nil)
	blockID, _ := json.Marshal(slackCardRef{Destination: "endpoint-7", ArtifactID: "visual-9", Version: 3, ComponentID: "many", Kind: cardChip})
	selected := cardEvents(t, cardIngress(t, adapter, clickPayload(slackBlock{"action_id": "openseal_card:3", "block_id": string(blockID), "selected_option": slackBlock{"value": "f"}})))
	vote, _ := json.Marshal(slackCardRef{Destination: "endpoint-7", ArtifactID: "visual-9", Version: 3, ComponentID: "vote", Kind: cardPoll, Element: "six"})
	voted := cardEvents(t, cardIngress(t, adapter, clickPayload(slackBlock{"action_id": "openseal_card:4", "value": string(vote)})))

	selectedValues, _ := json.Marshal(selected[0].Attributes["values"])
	votedValues, _ := json.Marshal(voted[0].Attributes["values"])
	if string(selectedValues) != `{"chipId":"f"}` || voted[0].Attributes["actionId"] != "vote" || string(votedValues) != `{"optionIds":["six"]}` {
		t.Fatalf("select = %#v, vote = %#v", selected[0].Attributes, voted[0].Attributes)
	}
}

func TestCardAnswerForAnotherEndpointIsDropped(t *testing.T) {
	adapter := newSlackAdapter("signing-secret", "", nil)
	other, _ := json.Marshal(slackCardRef{Destination: "endpoint-8", ArtifactID: "visual-9", Version: 3, ComponentID: "pick", Kind: cardChip, Element: "thai"})
	if events := cardEvents(t, cardIngress(t, adapter, clickPayload(slackBlock{"action_id": "openseal_card:1", "value": string(other)}))); len(events) != 0 {
		t.Fatalf("events = %#v", events)
	}
}

func TestCardFormOpensAModalAndItsSubmissionIsAnAnswer(t *testing.T) {
	var opened map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/views.open" || request.Header.Get("Authorization") != "Bearer xoxb-bot" {
			t.Errorf("request = %s %s", request.URL.Path, request.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(request.Body).Decode(&opened)
		_, _ = io.WriteString(response, `{"ok":true}`)
	}))
	defer server.Close()
	adapter := newSlackAdapter("signing-secret", server.URL, server.Client())
	button := renderedCardButton(t, "*RSVP*")

	clicked := cardIngress(t, adapter, clickPayload(slackBlock{"action_id": button["action_id"], "value": button["value"]}))

	view, _ := opened["view"].(map[string]interface{})
	if len(cardEvents(t, clicked)) != 0 || opened["trigger_id"] != "trigger-1" || view["callback_id"] != slackCardFormCallback {
		t.Fatalf("opened = %#v", opened)
	}
	blocks, _ := view["blocks"].([]interface{})
	name := blocks[0].(map[string]interface{})
	guests := blocks[1].(map[string]interface{})["element"].(map[string]interface{})
	if len(blocks) != 4 || name["block_id"] != "name" || name["optional"] != false || guests["type"] != "number_input" || guests["min_value"] != "0" {
		t.Fatalf("modal blocks = %#v", blocks)
	}

	submission := func(guests string) map[string]interface{} {
		return map[string]interface{}{
			"type": "view_submission", "api_app_id": "A123",
			"team": map[string]string{"id": "T123"}, "user": map[string]string{"id": "U9", "name": "Kev"},
			"view": map[string]interface{}{
				"id": "V1", "callback_id": slackCardFormCallback, "private_metadata": view["private_metadata"],
				"state": map[string]interface{}{"values": map[string]interface{}{
					"name":   map[string]interface{}{"v": map[string]interface{}{"type": "plain_text_input", "value": "Kev"}},
					"guests": map[string]interface{}{"v": map[string]interface{}{"type": "number_input", "value": guests}},
					"diet":   map[string]interface{}{"v": map[string]interface{}{"type": "static_select", "selected_option": map[string]string{"value": "vegan"}}},
					"agree":  map[string]interface{}{"v": map[string]interface{}{"type": "checkboxes", "selected_options": []map[string]string{{"value": "true"}}}},
				}},
			},
		}
	}
	submitted := cardIngress(t, adapter, submission("2"))
	events := cardEvents(t, submitted)
	if len(events) != 1 || !strings.Contains(bodyText(submitted), `"response_action":"clear"`) {
		t.Fatalf("submitted = %#v", submitted)
	}
	values, _ := json.Marshal(events[0].Attributes["values"])
	if events[0].Type != "conversation.card.interacted" || events[0].ExternalMessageID != "1720000000.1" || events[0].Attributes["componentId"] != "rsvp" ||
		string(values) != `{"agree":true,"diet":"vegan","guests":2,"name":"Kev"}` {
		t.Fatalf("event = %#v, values %s", events[0], values)
	}
	refused := cardIngress(t, adapter, submission("two"))
	if len(cardEvents(t, refused)) != 0 || !strings.Contains(bodyText(refused), "Enter a number") {
		t.Fatalf("bad number = %#v", refused)
	}
}

// A refreshed card is a message.update whose visual carries the answers.
func TestCardRefreshUpdatesTheMessageInPlace(t *testing.T) {
	var updated map[string]interface{}
	server := postedSlackBody(t, "/chat.update", &updated)
	defer server.Close()
	adapter := newSlackAdapter("", server.URL, server.Client())
	config := visualDeliveryConfig(t, "["+cardVisualJSON+"]")
	envelope := config[adapterEnvelopeKey].(map[string]interface{})
	envelope["endpoint"].(*conversationEndpoint).ID = "endpoint-7"
	delivery := envelope["delivery"].(*conversationDelivery)
	delivery.Operation = "message.update"
	delivery.Parameters = map[string]interface{}{"providerMessageId": "1720000000.1"}

	output, err := adapter.delivery(context.Background(), config)

	encoded, _ := json.Marshal(updated["blocks"])
	if err != nil || output["outcome"] != "delivered" || updated["ts"] != "1720000000.1" ||
		!strings.Contains(string(encoded), `"style":"primary"`) || !strings.Contains(string(encoded), "picked by 2 people") {
		t.Fatalf("update = %#v, %v", updated, err)
	}
}

func bodyText(output map[string]interface{}) string {
	switch body := output["body"].(type) {
	case []byte:
		return string(body)
	case string:
		return body
	}
	return ""
}
