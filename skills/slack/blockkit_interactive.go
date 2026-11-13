package main

import (
	"encoding/json"
	"strings"
)

// options draws the choices of choices, quiz, poll or actions as a static
// list; they cannot be picked from Slack yet.
func (r *blockRenderer) options(c visualComponent) []slackBlock {
	var options visualOptions
	if c.Type == "actions" {
		_ = json.Unmarshal(c.payload, &options.Options)
	} else if !decodePayload(c, &options) {
		return r.summary(c)
	}
	var lines []string
	for _, option := range append(options.Chips, options.Options...) {
		lines = append(lines, "• "+plain(option.Label))
	}
	lead := plain(c.Summary)
	if question := strings.TrimSpace(options.Question); question != "" {
		lead = bold(question)
	}
	return section(titled(c, joinLines(lead, listText(lines))))
}

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
	return section(titled(c, joinLines(plain(firstNonEmpty(form.Description, c.Summary)), listText(lines))))
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
