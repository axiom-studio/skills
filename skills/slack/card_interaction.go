package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A visual's interactive components are drawn as Block Kit elements whose
// action_id starts with slackCardActionPrefix. A click is normalized into a
// conversation.card.interacted event for the endpoint that sent the card;
// OpenSeal checks it against the stored visual and records it.
const (
	slackCardActionPrefix = "openseal_card"
	slackCardFormCallback = "openseal_card_form"
	slackCardEventType    = "conversation.card.interacted"
	// slackCardFieldAction is the action_id of every modal input; the input
	// block's block_id is the field id.
	slackCardFieldAction = "v"
	// slackCardManyField is the one input of a modal that picks several quiz
	// or poll options.
	slackCardManyField = "choice"
)

// Slack limits on what carries a card reference.
const (
	maxSlackButtonValue   = 2000
	maxSlackBlockID       = 255
	maxSlackOptionValue   = 150
	maxSlackOptionText    = 75
	maxSlackSelectOptions = 100
	maxSlackCheckboxes    = 10
	maxSlackViewMetadata  = 3000
	maxSlackViewTitle     = 24
)

// What a card element does when clicked.
const (
	cardChip     = "chip"      // choices: values {"chipId": element}
	cardAction   = "action"    // actions: action id element, no values
	cardQuiz     = "quiz"      // quiz: values {"selected": [element]}
	cardPoll     = "poll"      // poll: action "vote", values {"optionIds": [element]}
	cardForm     = "form"      // opens the form's modal
	cardQuizMany = "quiz_many" // opens a modal to pick several quiz options
	cardPollMany = "poll_many" // opens a modal to vote for several options
)

// slackCardRef names the card component an element answers: the endpoint
// that sent the card (Destination, which Socket Mode routes by), the
// visual version shown and the component. A button carries it whole in its
// value; a select carries it without Element in its block_id and the
// element in each option's value. A button that opens a modal also
// carries the modal's fields.
type slackCardRef struct {
	Destination string          `json:"d"`
	ArtifactID  string          `json:"a"`
	Version     int64           `json:"v"`
	ComponentID string          `json:"c"`
	Kind        string          `json:"k"`
	Element     string          `json:"e,omitempty"`
	Modal       *slackCardModal `json:"m,omitempty"`
}

type slackCardModal struct {
	Title  string           `json:"t"`
	Submit string           `json:"s,omitempty"`
	Fields []slackCardField `json:"f"`
}

// slackCardField is one modal input: a form field, or for a quiz or poll
// the options to pick (type "many").
type slackCardField struct {
	ID       string      `json:"i"`
	Type     string      `json:"y"`
	Label    string      `json:"l"`
	Required bool        `json:"r,omitempty"`
	Min      *float64    `json:"n,omitempty"`
	Max      *float64    `json:"x,omitempty"`
	Options  [][2]string `json:"o,omitempty"` // label, value
}

// slackCardViewMetadata is a card modal's private_metadata: the component
// it answers, where the card is, and each field's id and type to read the
// submission.
type slackCardViewMetadata struct {
	Ref       slackCardRef `json:"r"`
	ChannelID string       `json:"ch"`
	MessageID string       `json:"m"`
	ThreadID  string       `json:"th,omitempty"`
	Fields    [][2]string  `json:"f"` // id, type
}

func isSlackCardAction(actionID string) bool {
	return strings.HasPrefix(strings.TrimSpace(actionID), slackCardActionPrefix)
}

// decodeSlackCardRef reads the card reference of a clicked element: whole
// from a button's value, or from the actions block's block_id and the
// selected option's value.
func decodeSlackCardRef(value, blockID, selected string) (slackCardRef, bool) {
	var ref slackCardRef
	if json.Unmarshal([]byte(value), &ref) != nil || ref.ArtifactID == "" {
		if json.Unmarshal([]byte(blockID), &ref) != nil || ref.ArtifactID == "" {
			return slackCardRef{}, false
		}
		ref.Element = selected
	}
	if strings.TrimSpace(ref.Destination) == "" || ref.Version < 1 || strings.TrimSpace(ref.ComponentID) == "" {
		return slackCardRef{}, false
	}
	return ref, true
}

// slackCardInteractionPayload is the part of a card click or modal
// submission the adapter reads beyond slackInteraction.
type slackCardInteractionPayload struct {
	User struct {
		Name string `json:"name"`
	} `json:"user"`
	Actions []struct {
		BlockID        string `json:"block_id"`
		SelectedOption struct {
			Value string `json:"value"`
		} `json:"selected_option"`
	} `json:"actions"`
	View struct {
		State struct {
			Values map[string]map[string]slackCardInputState `json:"values"`
		} `json:"state"`
	} `json:"view"`
}

type slackCardInputState struct {
	Value          string `json:"value"`
	SelectedDate   string `json:"selected_date"`
	SelectedOption *struct {
		Value string `json:"value"`
	} `json:"selected_option"`
	SelectedOptions []struct {
		Value string `json:"value"`
	} `json:"selected_options"`
}

// normalizeSlackCardAction turns a card click into a card answer event, or
// opens the modal of a form or a several-option quiz or poll.
func (a *slackAdapter) normalizeSlackCardAction(ctx context.Context, envelope *adapterEnvelope, config map[string]interface{}, payload slackInteraction, raw []byte) (map[string]interface{}, error) {
	var extra slackCardInteractionPayload
	if json.Unmarshal(raw, &extra) != nil || len(extra.Actions) != 1 {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	action := payload.Actions[0]
	ref, ok := decodeSlackCardRef(action.Value, extra.Actions[0].BlockID, extra.Actions[0].SelectedOption.Value)
	if !ok {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	if ref.Destination != envelope.Endpoint.ID {
		return map[string]interface{}{"statusCode": http.StatusOK, "events": []interface{}{}}, nil
	}
	if ref.Modal != nil {
		return a.openSlackCardModal(ctx, config, payload, ref)
	}
	actionID, values, ok := slackCardAnswer(ref)
	if !ok {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	event := slackCardEvent(payload, extra.User.Name, ref, actionID, values,
		"slack:card:"+payload.Team.ID+":"+strings.TrimSpace(action.ActionTS)+":"+payload.User.ID,
		payload.Channel.ID, payload.Container.MessageTS, firstNonEmpty(payload.Container.ThreadTS, payload.Message.ThreadTS),
		slackTimestamp(firstNonEmpty(action.ActionTS, payload.ActionTS)))
	return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`, "events": []normalizedConversationEvent{event}}, nil
}

// slackCardAnswer is the action id and values a clicked element answers
// with, shaped as OpenSeal validates them.
func slackCardAnswer(ref slackCardRef) (string, map[string]interface{}, bool) {
	element := strings.TrimSpace(ref.Element)
	if element == "" {
		return "", nil, false
	}
	switch ref.Kind {
	case cardChip:
		return "", map[string]interface{}{"chipId": element}, true
	case cardAction:
		return element, nil, true
	case cardQuiz:
		return "", map[string]interface{}{"selected": []string{element}}, true
	case cardPoll:
		return "vote", map[string]interface{}{"optionIds": []string{element}}, true
	}
	return "", nil, false
}

func slackCardEvent(payload slackInteraction, name string, ref slackCardRef, actionID string, values map[string]interface{}, id, channelID, messageID, threadID string, occurredAt time.Time) normalizedConversationEvent {
	attributes := map[string]interface{}{
		"artifactId": ref.ArtifactID, "version": ref.Version, "componentId": ref.ComponentID, "teamId": payload.Team.ID,
	}
	if actionID != "" {
		attributes["actionId"] = actionID
	}
	if values != nil {
		attributes["values"] = values
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return normalizedConversationEvent{
		ID: id, Type: slackCardEventType, ExternalConversationID: channelID, ExternalThreadID: threadID,
		ExternalMessageID: messageID, ExternalParticipantID: payload.User.ID,
		ParticipantDisplayName: strings.TrimSpace(firstNonEmpty(name, payload.User.Username)),
		Direct:                 strings.HasPrefix(channelID, "D"),
		OrderingKey:            channelID + ":" + messageID, OccurredAt: occurredAt, Attributes: attributes,
	}
}

// openSlackCardModal opens the modal a card button carries. Slack must see
// views.open within three seconds of the click, so it is opened here.
func (a *slackAdapter) openSlackCardModal(ctx context.Context, config map[string]interface{}, payload slackInteraction, ref slackCardRef) (map[string]interface{}, error) {
	token, _ := config[slackConnectionKey].(string)
	if strings.TrimSpace(token) == "" || strings.TrimSpace(payload.TriggerID) == "" {
		return map[string]interface{}{"statusCode": http.StatusServiceUnavailable, "contentType": "text/plain", "body": []byte("Slack card forms are unavailable")}, nil
	}
	modal := *ref.Modal
	ref.Modal = nil
	metadata := slackCardViewMetadata{
		Ref: ref, ChannelID: payload.Channel.ID, MessageID: payload.Container.MessageTS,
		ThreadID: firstNonEmpty(payload.Container.ThreadTS, payload.Message.ThreadTS),
	}
	for _, field := range modal.Fields {
		metadata.Fields = append(metadata.Fields, [2]string{field.ID, field.Type})
	}
	encoded, err := json.Marshal(metadata)
	if err != nil || len(encoded) > maxSlackViewMetadata {
		return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": `{"ok":true}`, "events": []interface{}{}}, nil
	}
	view := slackCardModalView(modal, string(encoded))
	response, status, _, requestErr := a.slackJSON(ctx, strings.TrimSpace(token), http.MethodPost, "/views.open", nil, map[string]interface{}{"trigger_id": payload.TriggerID, "view": view})
	var opened struct {
		OK bool `json:"ok"`
	}
	if requestErr != nil || status < 200 || status >= 300 || json.Unmarshal(response, &opened) != nil || !opened.OK {
		return map[string]interface{}{"statusCode": http.StatusBadGateway, "contentType": "text/plain", "body": []byte("Slack could not open the card form")}, nil
	}
	return map[string]interface{}{"statusCode": http.StatusOK, "events": []interface{}{}}, nil
}

func slackCardModalView(modal slackCardModal, metadata string) map[string]interface{} {
	blocks := make([]slackBlock, 0, len(modal.Fields))
	for _, field := range modal.Fields {
		blocks = append(blocks, slackCardInput(field))
	}
	return map[string]interface{}{
		"type": "modal", "callback_id": slackCardFormCallback, "private_metadata": metadata,
		"title":  slackBlock{"type": "plain_text", "text": truncateRunes(firstNonEmpty(modal.Title, "Answer"), maxSlackViewTitle)},
		"submit": slackBlock{"type": "plain_text", "text": truncateRunes(firstNonEmpty(modal.Submit, "Send"), maxSlackViewTitle)},
		"close":  slackBlock{"type": "plain_text", "text": "Cancel"},
		"blocks": blocks,
	}
}

func slackCardInput(field slackCardField) slackBlock {
	element := slackBlock{"action_id": slackCardFieldAction}
	options := make([]slackBlock, 0, len(field.Options))
	for _, option := range field.Options {
		options = append(options, slackBlock{"text": slackBlock{"type": "plain_text", "text": truncateRunes(option[0], maxSlackOptionText)}, "value": option[1]})
	}
	switch field.Type {
	case "textarea":
		element["type"], element["multiline"] = "plain_text_input", true
	case "number":
		element["type"], element["is_decimal_allowed"] = "number_input", true
		if field.Min != nil {
			element["min_value"] = strconv.FormatFloat(*field.Min, 'f', -1, 64)
		}
		if field.Max != nil {
			element["max_value"] = strconv.FormatFloat(*field.Max, 'f', -1, 64)
		}
	case "select":
		element["type"], element["options"] = "static_select", options
	case "radio":
		element["type"], element["options"] = "radio_buttons", options
	case "checkbox":
		element["type"] = "checkboxes"
		element["options"] = []slackBlock{{"text": slackBlock{"type": "plain_text", "text": truncateRunes(field.Label, maxSlackOptionText)}, "value": "true"}}
	case "date":
		element["type"] = "datepicker"
	case "many":
		element["type"], element["options"] = "checkboxes", options
		if len(options) > maxSlackCheckboxes {
			element["type"] = "multi_static_select"
		}
	default:
		element["type"] = "plain_text_input"
	}
	return slackBlock{
		"type": "input", "block_id": field.ID, "optional": !field.Required, "element": element,
		"label": slackBlock{"type": "plain_text", "text": truncateRunes(field.Label, maxSlackSectionText)},
	}
}

// normalizeSlackCardSubmission turns a submitted card modal into a card
// answer event. A number Slack cannot read is shown as a field error.
func normalizeSlackCardSubmission(envelope *adapterEnvelope, payload slackInteraction, raw []byte) (map[string]interface{}, error) {
	var metadata slackCardViewMetadata
	var extra slackCardInteractionPayload
	if json.Unmarshal([]byte(payload.View.PrivateMetadata), &metadata) != nil || json.Unmarshal(raw, &extra) != nil ||
		metadata.Ref.ArtifactID == "" || metadata.Ref.Version < 1 || metadata.MessageID == "" {
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	if metadata.Ref.Destination != envelope.Endpoint.ID ||
		(envelope.Endpoint.Address != "" && envelope.Endpoint.Address != metadata.ChannelID) ||
		(stringConfiguration(envelope.Endpoint.Configuration, "teamId") != "" && stringConfiguration(envelope.Endpoint.Configuration, "teamId") != payload.Team.ID) {
		return map[string]interface{}{"statusCode": http.StatusOK, "events": []interface{}{}}, nil
	}
	values := map[string]interface{}{}
	invalid := map[string]string{}
	var picked []string
	for _, field := range metadata.Fields {
		id, kind := field[0], field[1]
		state := extra.View.State.Values[id][slackCardFieldAction]
		switch kind {
		case "number":
			if strings.TrimSpace(state.Value) == "" {
				continue
			}
			number, err := strconv.ParseFloat(strings.TrimSpace(state.Value), 64)
			if err != nil {
				invalid[id] = "Enter a number."
				continue
			}
			values[id] = number
		case "select", "radio":
			if state.SelectedOption != nil {
				values[id] = state.SelectedOption.Value
			}
		case "checkbox":
			values[id] = len(state.SelectedOptions) > 0
		case "date":
			if state.SelectedDate != "" {
				values[id] = state.SelectedDate
			}
		case "many":
			for _, option := range state.SelectedOptions {
				picked = append(picked, option.Value)
			}
		default:
			if strings.TrimSpace(state.Value) != "" {
				values[id] = state.Value
			}
		}
	}
	if len(invalid) > 0 {
		body, err := json.Marshal(map[string]interface{}{"response_action": "errors", "errors": invalid})
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": body, "events": []interface{}{}}, nil
	}
	actionID := ""
	switch metadata.Ref.Kind {
	case cardQuizMany:
		values = map[string]interface{}{"selected": picked}
	case cardPollMany:
		actionID, values = "vote", map[string]interface{}{"optionIds": picked}
	case cardForm:
	default:
		return map[string]interface{}{"statusCode": http.StatusBadRequest}, nil
	}
	event := slackCardEvent(payload, extra.User.Name, metadata.Ref, actionID, values,
		"slack:card:"+payload.Team.ID+":"+payload.View.ID+":"+payload.User.ID,
		metadata.ChannelID, metadata.MessageID, metadata.ThreadID, time.Now().UTC())
	body, err := json.Marshal(map[string]interface{}{"response_action": "clear"})
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"statusCode": http.StatusOK, "contentType": "application/json", "body": body, "events": []normalizedConversationEvent{event}}, nil
}

// slackSocketCardRoute is the endpoint a card click or card modal
// submission belongs to, or "" when the interaction is not a card's.
func slackSocketCardRoute(payload json.RawMessage) string {
	var interaction struct {
		Type    string `json:"type"`
		Actions []struct {
			ActionID string `json:"action_id"`
			Value    string `json:"value"`
			BlockID  string `json:"block_id"`
		} `json:"actions"`
		View struct {
			CallbackID      string `json:"callback_id"`
			PrivateMetadata string `json:"private_metadata"`
		} `json:"view"`
	}
	if json.Unmarshal(payload, &interaction) != nil {
		return ""
	}
	switch {
	case interaction.Type == "block_actions" && len(interaction.Actions) == 1 && isSlackCardAction(interaction.Actions[0].ActionID):
		ref, ok := decodeSlackCardRef(interaction.Actions[0].Value, interaction.Actions[0].BlockID, "")
		if !ok {
			return ""
		}
		return ref.Destination
	case interaction.Type == "view_submission" && interaction.View.CallbackID == slackCardFormCallback:
		var metadata slackCardViewMetadata
		if json.Unmarshal([]byte(interaction.View.PrivateMetadata), &metadata) != nil {
			return ""
		}
		return strings.TrimSpace(metadata.Ref.Destination)
	}
	return ""
}
