package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// maxSlackButtons is the most options drawn as buttons; more are a select,
// which stays one line however many options there are.
const maxSlackButtons = 5

// options draws choices, a quiz, a poll or actions: the lead, an actions
// block people answer with, and what people answered so far. Options that
// cannot be answered from Slack (no destination, a closed poll, actions
// that are not respond buttons, ids too long for Slack) draw as a list.
func (r *blockRenderer) options(c visualComponent) []slackBlock {
	var options visualOptions
	if c.Type == "actions" {
		_ = json.Unmarshal(c.payload, &options.Options)
	} else if !decodePayload(c, &options) {
		return r.summary(c)
	}
	choices := append(options.Chips, options.Options...)
	lead := plain(c.Summary)
	if question := strings.TrimSpace(options.Question); question != "" {
		lead = bold(question)
	}
	answered := contextBlocks(answeredText(c.Type, choices, r.answers(c.ID)))
	if actions := r.optionActions(c, options, choices); actions != nil {
		return append(append(section(titled(c, lead)), actions), answered...)
	}
	var lines []string
	for _, option := range choices {
		lines = append(lines, "• "+plain(option.Label))
	}
	return append(section(titled(c, joinLines(lead, listText(lines)))), answered...)
}

// optionActions is the actions block that answers c, or nil. Up to
// maxSlackButtons options are buttons, more are a select; a quiz or poll
// that takes several options is one button opening a modal to pick them.
// Options someone chose are primary buttons.
func (r *blockRenderer) optionActions(c visualComponent, options visualOptions, choices []visualLabeled) slackBlock {
	if r.destination == "" || len(choices) == 0 {
		return nil
	}
	kind := ""
	switch c.Type {
	case "choices":
		kind = cardChip
	case "actions":
		// Approval, entity and edit buttons keep their own flows.
		for _, choice := range choices {
			if choice.Kind != "respond" {
				return nil
			}
		}
		kind = cardAction
	case "quiz":
		kind = cardQuiz
		if options.AllowMultiple {
			return r.modalButton(c, cardQuizMany, "Answer", slackCardModal{Title: "Answer", Fields: []slackCardField{manyField(options.Question, choices)}})
		}
	case "poll":
		if closes, err := time.Parse(time.RFC3339, options.ClosesAt); err == nil && !r.now.Before(closes) {
			return nil
		}
		kind = cardPoll
		if options.Multiple {
			return r.modalButton(c, cardPollMany, "Vote", slackCardModal{Title: "Vote", Submit: "Vote", Fields: []slackCardField{manyField(options.Question, choices)}})
		}
	default:
		return nil
	}
	chosen := map[string]bool{}
	if answers := r.answers(c.ID); answers != nil {
		for _, choice := range answers.Chosen {
			chosen[choice.ID] = true
		}
	}
	if len(choices) <= maxSlackButtons {
		return r.buttons(c, kind, choices, chosen)
	}
	return r.selectMenu(c, kind, choices)
}

func (r *blockRenderer) buttons(c visualComponent, kind string, choices []visualLabeled, chosen map[string]bool) slackBlock {
	elements := make([]slackBlock, 0, len(choices))
	for _, choice := range choices {
		ref := r.cardRef(c, kind)
		ref.Element = choice.ID
		button, ok := r.button(choice.Label, ref)
		if !ok {
			return nil
		}
		if chosen[choice.ID] {
			button["style"] = "primary"
		}
		elements = append(elements, button)
	}
	return slackBlock{"type": "actions", "elements": elements}
}

// selectMenu carries the card reference in the block id and each option's
// id in its value, since an option value holds only 150 characters.
func (r *blockRenderer) selectMenu(c visualComponent, kind string, choices []visualLabeled) slackBlock {
	blockID, err := json.Marshal(r.cardRef(c, kind))
	if err != nil || len(blockID) > maxSlackBlockID || len(choices) > maxSlackSelectOptions {
		return nil
	}
	options := make([]slackBlock, 0, len(choices))
	for _, choice := range choices {
		if len(choice.ID) > maxSlackOptionValue {
			return nil
		}
		options = append(options, slackBlock{"text": slackBlock{"type": "plain_text", "text": truncateRunes(firstNonEmpty(choice.Label, choice.ID), maxSlackOptionText)}, "value": choice.ID})
	}
	return slackBlock{"type": "actions", "block_id": string(blockID), "elements": []slackBlock{{
		"type": "static_select", "action_id": r.actionID(),
		"placeholder": slackBlock{"type": "plain_text", "text": "Choose…"}, "options": options,
	}}}
}

// modalButton is an actions block with one button that opens modal.
func (r *blockRenderer) modalButton(c visualComponent, kind, label string, modal slackCardModal) slackBlock {
	ref := r.cardRef(c, kind)
	ref.Modal = &modal
	button, ok := r.button(label, ref)
	if !ok {
		return nil
	}
	return slackBlock{"type": "actions", "elements": []slackBlock{button}}
}

// button carries ref whole in its value; a reference too long for a
// button value cannot be answered from Slack.
func (r *blockRenderer) button(label string, ref slackCardRef) (slackBlock, bool) {
	value, err := json.Marshal(ref)
	if err != nil || len(value) > maxSlackButtonValue {
		return nil, false
	}
	return slackBlock{
		"type": "button", "action_id": r.actionID(), "value": string(value),
		"text": slackBlock{"type": "plain_text", "text": truncateRunes(firstNonEmpty(label, "Choose"), maxSlackOptionText), "emoji": true},
	}, true
}

func (r *blockRenderer) cardRef(c visualComponent, kind string) slackCardRef {
	return slackCardRef{Destination: r.destination, ArtifactID: r.current.ArtifactID, Version: r.current.Version, ComponentID: c.ID, Kind: kind}
}

func (r *blockRenderer) actionID() string {
	r.elements++
	return slackCardActionPrefix + ":" + strconv.Itoa(r.elements)
}

func (r *blockRenderer) answers(componentID string) *visualAnswers {
	for index := range r.current.Answers {
		if r.current.Answers[index].ComponentID == componentID {
			return &r.current.Answers[index]
		}
	}
	return nil
}

func manyField(question string, choices []visualLabeled) slackCardField {
	field := slackCardField{ID: slackCardManyField, Type: "many", Label: firstNonEmpty(question, "Pick"), Required: true}
	for _, choice := range choices {
		field.Options = append(field.Options, [2]string{firstNonEmpty(choice.Label, choice.ID), choice.ID})
	}
	return field
}

// answeredText says what people answered: each chosen option with its
// count, and how many people answered. A form says only how many sent it.
func answeredText(componentType string, choices []visualLabeled, answers *visualAnswers) string {
	if answers == nil || answers.People == 0 {
		return ""
	}
	people := strconv.Itoa(answers.People) + " people"
	if answers.People == 1 {
		people = "1 person"
	}
	if componentType == "form" {
		return ":white_check_mark: Sent by " + people
	}
	labels := map[string]string{}
	for _, choice := range choices {
		labels[choice.ID] = choice.Label
	}
	parts := make([]string, 0, len(answers.Chosen))
	for _, choice := range answers.Chosen {
		parts = append(parts, plain(firstNonEmpty(labels[choice.ID], choice.ID))+" "+strconv.Itoa(choice.Count))
	}
	verb := map[string]string{"choices": "Picked", "actions": "Chosen", "quiz": "Answered", "poll": "Votes"}[componentType]
	return ":white_check_mark: " + joinWith(" · ", parts...) + " — " + strings.ToLower(firstNonEmpty(verb, "Answered")) + " by " + people
}

// form draws the form's fields as a list and, when answers can come back,
// a button that opens the form as a modal.
func (r *blockRenderer) form(c visualComponent) []slackBlock {
	var form visualForm
	if !decodePayload(c, &form) {
		return r.summary(c)
	}
	lines := make([]string, 0, len(form.Fields))
	for _, field := range form.Fields {
		line := "• " + bold(field.Label)
		if field.Required {
			line += " (required)"
		}
		var choices []string
		for _, option := range field.Options {
			choices = append(choices, option.Label)
		}
		if len(choices) > 0 {
			line += ": " + plain(strings.Join(choices, " / "))
		}
		lines = append(lines, line)
	}
	blocks := section(titled(c, joinLines(plain(firstNonEmpty(form.Description, c.Summary)), listText(lines))))
	if open := r.formButton(c, form); open != nil {
		blocks = append(blocks, open)
	}
	return append(blocks, contextBlocks(answeredText("form", nil, r.answers(c.ID)))...)
}

// formButton opens the form as a modal. A form whose fields do not fit a
// button value, or whose option values Slack cannot hold, stays a list.
func (r *blockRenderer) formButton(c visualComponent, form visualForm) slackBlock {
	if r.destination == "" || len(form.Fields) == 0 {
		return nil
	}
	modal := slackCardModal{Title: firstNonEmpty(c.Title, "Form"), Submit: form.SubmitLabel}
	for _, field := range form.Fields {
		input := slackCardField{ID: field.ID, Type: field.Type, Label: field.Label, Required: field.Required, Min: field.Min, Max: field.Max}
		if len(field.ID) > maxSlackBlockID || (len(field.Options) > maxSlackSelectOptions) {
			return nil
		}
		for _, option := range field.Options {
			if len(option.Value) > maxSlackOptionValue {
				return nil
			}
			input.Options = append(input.Options, [2]string{firstNonEmpty(option.Label, option.Value), option.Value})
		}
		modal.Fields = append(modal.Fields, input)
	}
	return r.modalButton(c, cardForm, "Fill in", modal)
}

func (r *blockRenderer) flashcards(c visualComponent) []slackBlock {
	var deck visualFlashcards
	if !decodePayload(c, &deck) {
		return r.summary(c)
	}
	lines := make([]string, 0, len(deck.Cards))
	for _, card := range deck.Cards {
		lines = append(lines, "• "+plain(card.Front))
	}
	return summaryWithList(c, lines)
}

func (r *blockRenderer) explorer(c visualComponent) []slackBlock {
	var explorer visualExplorer
	if !decodePayload(c, &explorer) {
		return r.summary(c)
	}
	var lines []string
	for _, input := range explorer.Inputs {
		value := ""
		if input.Default != nil {
			value = ": " + plain(joinWith(" ", groupedNumber(*input.Default), input.Unit))
		}
		lines = append(lines, "• "+bold(input.Label)+value)
	}
	for _, output := range explorer.Outputs {
		lines = append(lines, "→ "+plain(output.Label))
	}
	return summaryWithList(c, lines)
}

func (r *blockRenderer) draft(c visualComponent) []slackBlock {
	var draft visualDraft
	if !decodePayload(c, &draft) {
		return r.summary(c)
	}
	head := plain(c.Summary)
	if subject := strings.TrimSpace(draft.Subject); subject != "" {
		head = joinLines(head, bold("Subject:")+" "+plain(subject))
	}
	body := "> " + strings.ReplaceAll(markdownToSlack(draft.Body), "\n", "\n> ")
	return section(titled(c, joinLines(head, body)))
}
