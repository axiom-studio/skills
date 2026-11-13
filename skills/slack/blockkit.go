package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Block Kit limits the renderer keeps (docs.slack.dev/reference/block-kit).
const (
	maxSlackBlocks      = 50
	maxSlackSectionText = 3000
	maxSlackFieldText   = 2000
	maxSlackFields      = 10
	maxSlackHeaderText  = 150
	// Slack recommends at most 4000 characters of top-level message text.
	maxSlackMessageText  = 4000
	maxSlackTableRows    = 100
	maxSlackTableColumns = 20
	maxSlackTableChars   = 10000
	// maxSlackListLines bounds a list before it says how many more it has.
	maxSlackListLines = 20
)

const slackFullCardNotice = "…see the full card in HelloSeal"

type slackBlock = map[string]interface{}

// slackVisualMessage is a message that shows answer followed by its
// visuals as Block Kit blocks, and its top-level text: the answer and each
// visual's fallback text, which Slack shows in notifications and screen
// readers.
// Answerable components draw as buttons, selects and form buttons when
// destination, the conversation endpoint that sends the message, is known.
func slackVisualMessage(answer string, visuals []conversationVisual, destination string) ([]slackBlock, string) {
	var blocks []slackBlock
	for _, chunk := range splitSlackText(markdownToSlack(answer), maxSlackSectionText) {
		blocks = append(blocks, sectionBlock(chunk))
	}
	renderer := &blockRenderer{destination: destination, now: time.Now()}
	for _, visual := range visuals {
		blocks = append(blocks, renderer.visual(visual)...)
	}
	return limitSlackBlocks(blocks), slackVisualText(answer, visuals)
}

// slackVisualText is answer followed by its visuals' fallback texts, as
// Slack mrkdwn bounded to the recommended message text length.
func slackVisualText(answer string, visuals []conversationVisual) string {
	parts := []string{answer}
	for _, visual := range visuals {
		parts = append(parts, visual.FallbackText)
	}
	return truncateRunes(markdownToSlack(strings.Join(nonEmptyStrings(parts), "\n\n")), maxSlackMessageText)
}

// limitSlackBlocks keeps a message within Slack's block count, ending a cut
// message with a notice that the card has more.
func limitSlackBlocks(blocks []slackBlock) []slackBlock {
	if len(blocks) <= maxSlackBlocks {
		return blocks
	}
	return append(blocks[:maxSlackBlocks-1:maxSlackBlocks-1], contextBlock(slackFullCardNotice))
}

// blockRenderer draws the visuals of one message. Every component type has
// one renderer method that returns its blocks. Choices, quizzes, polls,
// respond actions and forms draw as elements people answer with
// (blockkit_interactive.go); flashcards, explorers and drafts draw as text.
type blockRenderer struct {
	current conversationVisual
	// destination is the endpoint card answers go back to; without it
	// every component draws as text.
	destination string
	now         time.Time
	// elements numbers the card elements so each action_id is unique in
	// the message.
	elements int
	// tableShown records that the message already holds a table block;
	// Slack allows one, so later tables draw as text.
	tableShown bool
}

func (r *blockRenderer) visual(visual conversationVisual) []slackBlock {
	r.current = visual
	var blocks []slackBlock
	if title := strings.TrimSpace(visual.Surface.Title); title != "" {
		blocks = append(blocks, headerBlock(title))
	}
	for _, component := range visual.Surface.Components {
		blocks = append(blocks, r.component(component)...)
	}
	return blocks
}

func (r *blockRenderer) component(c visualComponent) []slackBlock {
	switch c.Type {
	case "text":
		return section(titled(c, markdownToSlack(c.Text)))
	case "caption":
		return contextBlocks(markdownToSlack(c.Text))
	case "divider":
		return append([]slackBlock{{"type": "divider"}}, contextBlocks(plain(c.Text))...)
	case "section", "grid", "carousel":
		return r.children(section(titled(c, "")), c.Children)
	case "tabs":
		blocks := section(titled(c, ""))
		for _, tab := range c.Tabs {
			blocks = r.children(append(blocks, contextBlocks(bold(tab.Label))...), tab.Children)
		}
		return blocks
	case "callout":
		return r.callout(c)
	case "stat_row":
		return r.statRow(c)
	case "table":
		return r.table(c)
	case "item":
		return r.item(c)
	case "place":
		return r.place(c)
	case "definition":
		return r.definition(c)
	case "steps":
		return r.steps(c)
	case "checklist":
		return r.checklist(c)
	case "timeline":
		return r.timeline(c)
	case "agenda":
		return r.agenda(c)
	case "itinerary":
		return r.itinerary(c)
	case "recipe":
		return r.recipe(c)
	case "progress":
		return r.progress(c)
	case "tasks":
		return r.tasks(c)
	case "routine":
		return r.routine(c)
	case "agent":
		return r.agent(c)
	case "image", "gallery", "labeled_image":
		return r.images(c)
	case "choices", "quiz", "poll", "actions":
		return r.options(c)
	case "form":
		return r.form(c)
	case "flashcards":
		return r.flashcards(c)
	case "explorer":
		return r.explorer(c)
	case "draft":
		return r.draft(c)
	case "chart":
		return r.chart(c)
	case "breakdown":
		return r.breakdown(c)
	case "comparison":
		return r.comparison(c)
	case "diagram":
		return r.diagram(c)
	case "map":
		return r.pins(c)
	case "math":
		return r.math(c)
	case "before_after":
		return r.beforeAfter(c)
	case "analogy":
		return r.analogy(c)
	default:
		// quantity_visual and any type this renderer does not know.
		return r.summary(c)
	}
}

func (r *blockRenderer) children(blocks []slackBlock, children []visualComponent) []slackBlock {
	for _, child := range children {
		blocks = append(blocks, r.component(child)...)
	}
	return blocks
}

// summary draws a component as its title and summary only.
func (r *blockRenderer) summary(c visualComponent) []slackBlock {
	return section(titled(c, plain(c.Summary)))
}

// summaryWithFields draws a component as its title and summary with facts
// as section fields.
func summaryWithFields(c visualComponent, fields []string) []slackBlock {
	return sectionWithFields(titled(c, plain(c.Summary)), fields)
}

// summaryWithList draws a component as its title and summary followed by
// lines, in one section.
func summaryWithList(c visualComponent, lines []string) []slackBlock {
	return section(titled(c, joinLines(plain(c.Summary), listText(lines))))
}

var calloutIcons = map[string]string{
	"positive": ":white_check_mark:", "negative": ":no_entry:", "warning": ":warning:",
	"info": ":information_source:", "accent": ":star:", "neutral": ":bulb:",
}

func (r *blockRenderer) callout(c visualComponent) []slackBlock {
	var callout visualCallout
	if !decodePayload(c, &callout) {
		return r.summary(c)
	}
	icon := calloutIcons[c.Tone]
	if icon == "" {
		icon = calloutIcons["info"]
	}
	return section(icon + " " + titled(c, markdownToSlack(callout.Body)))
}

var trendArrows = map[string]string{"up": " ↑", "down": " ↓", "flat": " →"}

func (r *blockRenderer) statRow(c visualComponent) []slackBlock {
	var row visualStatRow
	if !decodePayload(c, &row) {
		return r.summary(c)
	}
	fields := make([]string, 0, len(row.Stats))
	for _, stat := range row.Stats {
		value := amountText(stat.visualAmount)
		if delta := strings.TrimSpace(stat.Delta); delta != "" {
			value += " (" + delta + ")"
		}
		fields = append(fields, bold(stat.Label)+"\n"+plain(value)+trendArrows[stat.Trend])
	}
	return sectionWithFields(titled(c, ""), fields)
}

func (r *blockRenderer) item(c visualComponent) []slackBlock {
	var item visualItem
	if !decodePayload(c, &item) {
		return r.summary(c)
	}
	head := bold(item.Name)
	if badge := strings.TrimSpace(item.Badge); badge != "" {
		head += "  `" + plain(badge) + "`"
	}
	return sectionWithFields(titled(c, joinLines(head, plain(joinWith(" · ", priceText(item.Price), ratingText(item.Rating))))), plainAll(item.Facts))
}

func (r *blockRenderer) place(c visualComponent) []slackBlock {
	var place visualPlace
	if !decodePayload(c, &place) {
		return r.summary(c)
	}
	head := bold(place.Name)
	if category := strings.TrimSpace(place.Category); category != "" {
		head = joinWith(" · ", head, "_"+plain(category)+"_")
	}
	fields := plainAll(place.Facts)
	if len(place.Hours) > 0 {
		fields = append(fields, bold("Hours")+"\n"+plain(strings.Join(place.Hours, "\n")))
	}
	return sectionWithFields(titled(c, joinLines(head, plain(place.Address), plain(ratingText(place.Rating)))), fields)
}

func (r *blockRenderer) definition(c visualComponent) []slackBlock {
	var definition visualDefinition
	if !decodePayload(c, &definition) {
		return r.summary(c)
	}
	head := bold(definition.Term)
	for _, extra := range []string{definition.Phonetic, definition.PartOfSpeech} {
		if strings.TrimSpace(extra) != "" {
			head += " _" + plain(extra) + "_"
		}
	}
	example := ""
	if strings.TrimSpace(definition.Example) != "" {
		example = "> " + plain(definition.Example)
	}
	return section(titled(c, joinLines(head, plain(definition.Meaning), example)))
}

func (r *blockRenderer) steps(c visualComponent) []slackBlock {
	var steps visualSteps
	if !decodePayload(c, &steps) {
		return r.summary(c)
	}
	lines := make([]string, 0, len(steps.Items))
	for index, step := range steps.Items {
		marker := strconv.Itoa(index+1) + "."
		if steps.Checkable {
			marker = "☐"
		}
		lines = append(lines, marker+" "+joinWith(" — ", bold(step.Title), markdownToSlack(step.Detail)))
	}
	return section(titled(c, listText(lines)))
}

func (r *blockRenderer) checklist(c visualComponent) []slackBlock {
	var checklist struct {
		Items []visualLabeled `json:"items"`
	}
	if !decodePayload(c, &checklist) {
		return r.summary(c)
	}
	var lines []string
	group := ""
	for _, item := range checklist.Items {
		if item.Group != "" && item.Group != group {
			group = item.Group
			lines = append(lines, bold(group))
		}
		lines = append(lines, "☐ "+plain(item.Label))
	}
	return section(titled(c, listText(lines)))
}

func (r *blockRenderer) timeline(c visualComponent) []slackBlock {
	var timeline visualTimeline
	if !decodePayload(c, &timeline) {
		return r.summary(c)
	}
	lines := make([]string, 0, len(timeline.Events))
	for _, event := range timeline.Events {
		lines = append(lines, "• "+joinWith(" — ", bold(event.When), plain(event.Title), markdownToSlack(event.Detail)))
	}
	return section(titled(c, listText(lines)))
}

func (r *blockRenderer) agenda(c visualComponent) []slackBlock {
	var agenda visualAgenda
	if !decodePayload(c, &agenda) {
		return r.summary(c)
	}
	blocks := section(titled(c, ""))
	for _, day := range agenda.Days {
		lines := []string{bold(firstNonEmpty(day.Label, day.Date))}
		for _, item := range day.Items {
			when := joinWith("–", item.Start, item.End)
			lines = append(lines, "• "+joinWith(" ", plain(when), plain(item.Title), italic(item.Place)))
		}
		blocks = append(blocks, section(listText(lines))...)
	}
	return blocks
}

func (r *blockRenderer) itinerary(c visualComponent) []slackBlock {
	var itinerary visualItinerary
	if !decodePayload(c, &itinerary) {
		return r.summary(c)
	}
	blocks := section(titled(c, ""))
	for _, day := range itinerary.Days {
		lines := []string{bold(day.Label)}
		for _, item := range day.Items {
			lines = append(lines, "• "+joinWith(" ", plain(item.Time), plain(item.Title), italic(item.Place)))
		}
		blocks = append(blocks, section(listText(lines))...)
	}
	return blocks
}

func (r *blockRenderer) recipe(c visualComponent) []slackBlock {
	var recipe visualRecipe
	if !decodePayload(c, &recipe) {
		return r.summary(c)
	}
	ingredients := []string{bold(fmt.Sprintf("Ingredients (serves %d)", recipe.Servings))}
	for _, ingredient := range recipe.Ingredients {
		quantity := ""
		if ingredient.Qty != nil {
			quantity = groupedNumber(*ingredient.Qty)
		}
		ingredients = append(ingredients, "• "+plain(joinWith(" ", quantity, ingredient.Unit, ingredient.Name)))
	}
	steps := []string{bold("Steps")}
	for index, step := range recipe.Steps {
		steps = append(steps, strconv.Itoa(index+1)+". "+plain(step.Text))
	}
	return append(section(titled(c, listText(ingredients))), section(listText(steps))...)
}

func (r *blockRenderer) progress(c visualComponent) []slackBlock {
	var progress visualProgress
	if !decodePayload(c, &progress) {
		return r.summary(c)
	}
	lines := make([]string, 0, len(progress.Items))
	for _, item := range progress.Items {
		amount := func(value float64) string {
			return amountText(visualAmount{Value: value, Format: item.Format, Currency: item.Currency, Unit: item.Unit})
		}
		lines = append(lines, bold(item.Label)+"\n`"+progressBar(item.Value, item.Target)+"` "+plain(amount(item.Value)+" of "+amount(item.Target)))
	}
	return section(titled(c, listText(lines)))
}

// progressBar draws value against target as ten cells and a percentage.
func progressBar(value, target float64) string {
	share := 0.0
	if target > 0 {
		share = math.Max(0, math.Min(1, value/target))
	}
	filled := int(math.Round(share * 10))
	return strings.Repeat("▓", filled) + strings.Repeat("░", 10-filled) + " " + strconv.Itoa(int(math.Round(share*100))) + "%"
}

func (r *blockRenderer) tasks(c visualComponent) []slackBlock {
	var tasks visualTasks
	if !decodePayload(c, &tasks) {
		return r.summary(c)
	}
	lines := make([]string, 0, len(tasks.Rows))
	for _, row := range tasks.Rows {
		lines = append(lines, "• "+plain(row.Label))
	}
	return summaryWithList(c, lines)
}

func (r *blockRenderer) routine(c visualComponent) []slackBlock {
	var routine visualRoutine
	_ = decodePayload(c, &routine)
	lines := make([]string, 0, len(routine.Items))
	for _, item := range routine.Items {
		lines = append(lines, "• "+bold(item.Label)+" "+plain(item.Text))
	}
	return summaryWithList(c, lines)
}

func (r *blockRenderer) agent(c visualComponent) []slackBlock {
	var agent visualAgent
	if !decodePayload(c, &agent) {
		return r.summary(c)
	}
	lines := []string{bold(agent.Headline)}
	for _, point := range agent.Points {
		lines = append(lines, "• "+plain(point))
	}
	return section(titled(c, listText(lines)))
}

// images draws an image, gallery or labeled image as its caption: the
// pictures are chat artifacts with no public URL a Slack image block could
// load.
func (r *blockRenderer) images(c visualComponent) []slackBlock {
	var images visualImages
	_ = decodePayload(c, &images)
	captions := []string{images.Caption}
	for _, image := range images.Images {
		captions = append(captions, image.Caption)
	}
	caption := firstNonEmpty(joinWith(" · ", captions...), c.Summary, c.Title)
	blocks := contextBlocks(":frame_with_picture: " + plain(caption))
	if len(images.Markers) > 0 {
		lines := make([]string, 0, len(images.Markers))
		for index, marker := range images.Markers {
			lines = append(lines, strconv.Itoa(index+1)+". "+joinWith(" — ", bold(marker.Label), markdownToSlack(marker.Detail)))
		}
		blocks = append(blocks, section(listText(lines))...)
	}
	return blocks
}

func (r *blockRenderer) chart(c visualComponent) []slackBlock {
	var chart visualChart
	if !decodePayload(c, &chart) {
		return r.summary(c)
	}
	var fields []string
	for _, series := range chart.Series {
		prefix := ""
		if len(chart.Series) > 1 {
			prefix = series.Name + " "
		}
		for _, point := range series.Points {
			amount := chart.visualAmount
			amount.Value = point.Value
			fields = append(fields, bold(prefix+point.Label)+"\n"+plain(amountText(amount)))
		}
	}
	return summaryWithFields(c, fields)
}

func (r *blockRenderer) breakdown(c visualComponent) []slackBlock {
	var breakdown visualBreakdown
	if !decodePayload(c, &breakdown) {
		return r.summary(c)
	}
	fields := make([]string, 0, len(breakdown.Items))
	for _, item := range breakdown.Items {
		amount := breakdown.visualAmount
		amount.Value = item.Value
		fields = append(fields, bold(item.Label)+"\n"+plain(amountText(amount)))
	}
	return summaryWithFields(c, fields)
}

func (r *blockRenderer) comparison(c visualComponent) []slackBlock {
	var comparison visualComparison
	if !decodePayload(c, &comparison) {
		return r.summary(c)
	}
	fields := make([]string, 0, len(comparison.Options))
	for _, option := range comparison.Options {
		head := bold(option.Name)
		if option.ID == comparison.Recommended {
			head += " :star:"
		}
		lines := []string{head, plain(joinWith(" · ", option.Badge, priceText(option.Price)))}
		for _, attribute := range comparison.Attributes {
			if value := strings.TrimSpace(option.Values[attribute.Key]); value != "" {
				lines = append(lines, plain(attribute.Label+": "+value))
			}
		}
		fields = append(fields, joinLines(lines...))
	}
	return summaryWithFields(c, fields)
}

func (r *blockRenderer) diagram(c visualComponent) []slackBlock {
	var diagram visualDiagram
	if !decodePayload(c, &diagram) {
		return r.summary(c)
	}
	labels := map[string]string{}
	var nodes []string
	for _, node := range diagram.Nodes {
		labels[node.ID] = node.Label
		nodes = append(nodes, "• "+plain(node.Label))
	}
	if len(diagram.Edges) == 0 {
		return summaryWithList(c, nodes)
	}
	lines := make([]string, 0, len(diagram.Edges))
	for _, edge := range diagram.Edges {
		line := "• " + plain(labels[edge.From]+" → "+labels[edge.To])
		if label := strings.TrimSpace(edge.Label); label != "" {
			line += " _" + plain(label) + "_"
		}
		lines = append(lines, line)
	}
	return summaryWithList(c, lines)
}

func (r *blockRenderer) pins(c visualComponent) []slackBlock {
	var pins visualMap
	if !decodePayload(c, &pins) {
		return r.summary(c)
	}
	lines := make([]string, 0, len(pins.Pins))
	for _, pin := range pins.Pins {
		lines = append(lines, ":round_pushpin: "+plain(firstNonEmpty(pin.Label, pin.Place)))
	}
	return summaryWithList(c, lines)
}

func (r *blockRenderer) math(c visualComponent) []slackBlock {
	var worked visualMath
	if !decodePayload(c, &worked) {
		return r.summary(c)
	}
	lines := make([]string, 0, len(worked.Steps))
	for _, step := range worked.Steps {
		lines = append(lines, joinWith("  ", "`"+plain(strings.ReplaceAll(step.LaTeX, "`", "'"))+"`", italic(step.Note)))
	}
	return summaryWithList(c, lines)
}

func (r *blockRenderer) beforeAfter(c visualComponent) []slackBlock {
	var change visualBeforeAfter
	if !decodePayload(c, &change) {
		return r.summary(c)
	}
	before, after := "Before", "After"
	if change.Labels != nil {
		before, after = firstNonEmpty(change.Labels.Before, before), firstNonEmpty(change.Labels.After, after)
	}
	var fields []string
	if strings.TrimSpace(change.Before.Text+change.After.Text) != "" {
		fields = []string{bold(before) + "\n" + plain(change.Before.Text), bold(after) + "\n" + plain(change.After.Text)}
	}
	return summaryWithFields(c, fields)
}

func (r *blockRenderer) analogy(c visualComponent) []slackBlock {
	var analogy visualAnalogy
	if !decodePayload(c, &analogy) {
		return r.summary(c)
	}
	lines := []string{bold(analogy.From.Label) + " ↔ " + bold(analogy.To.Label)}
	for _, pair := range analogy.Mapping {
		lines = append(lines, "• "+plain(pair.A+" → "+pair.B))
	}
	return summaryWithList(c, lines)
}

// table draws the message's first table as a Slack table block and any
// later one as monospace text.
func (r *blockRenderer) table(c visualComponent) []slackBlock {
	var table visualTable
	if !decodePayload(c, &table) || len(table.Columns) == 0 {
		return r.summary(c)
	}
	header := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		header = append(header, column.Label)
	}
	rows := [][]string{header}
	for _, row := range table.Rows {
		cells := make([]string, 0, len(table.Columns))
		for _, column := range table.Columns {
			cells = append(cells, tableCellText(column.Kind, column.Currency, row[column.Key]))
		}
		rows = append(rows, cells)
	}
	blocks := section(titled(c, ""))
	if r.tableShown || len(table.Columns) > maxSlackTableColumns {
		return append(blocks, monospaceTable(rows)...)
	}
	r.tableShown = true
	shown, chars := 0, 0
	for _, row := range rows {
		size := 0
		for _, cell := range row {
			size += utf8.RuneCountInString(cell)
		}
		if shown == maxSlackTableRows || chars+size > maxSlackTableChars {
			break
		}
		shown, chars = shown+1, chars+size
	}
	cells := make([][]slackBlock, 0, shown)
	for _, row := range rows[:shown] {
		line := make([]slackBlock, 0, len(row))
		for _, cell := range row {
			line = append(line, slackBlock{"type": "raw_text", "text": cell})
		}
		cells = append(cells, line)
	}
	settings := make([]slackBlock, 0, len(table.Columns))
	for _, column := range table.Columns {
		align := "left"
		if column.Kind == "number" || column.Kind == "currency" || column.Kind == "percent" {
			align = "right"
		}
		settings = append(settings, slackBlock{"align": align, "is_wrapped": true})
	}
	blocks = append(blocks, slackBlock{"type": "table", "rows": cells, "column_settings": settings})
	if hidden := len(rows) - shown; hidden > 0 {
		blocks = append(blocks, contextBlocks(fmt.Sprintf("…and %d more rows", hidden))...)
	}
	return blocks
}

func monospaceTable(rows [][]string) []slackBlock {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, strings.Join(row, " | "))
	}
	text := truncateRunes(slackEscape(strings.Join(lines, "\n")), maxSlackSectionText-8)
	return section("```\n" + text + "\n```")
}

// tableCellText is a cell as text: a string as is, a number in its
// column's format, and an image reference as a placeholder.
func tableCellText(kind, currency string, raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return firstNonEmpty(text, "–")
	}
	var number float64
	if json.Unmarshal(raw, &number) == nil {
		format := ""
		if kind == "currency" || kind == "percent" {
			format = kind
		}
		return amountText(visualAmount{Value: number, Format: format, Currency: currency})
	}
	if len(raw) > 0 {
		return "(image)"
	}
	return "–"
}

// Block builders. Each keeps Slack's limits and returns no block for empty
// text, since Slack rejects empty text objects.

func section(text string) []slackBlock {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []slackBlock{sectionBlock(text)}
}

func sectionBlock(text string) slackBlock {
	return slackBlock{"type": "section", "text": mrkdwn(truncateRunes(text, maxSlackSectionText))}
}

// sectionWithFields is a section of text and up to maxSlackFields fields;
// the last field says how many more were left out.
func sectionWithFields(text string, fields []string) []slackBlock {
	fields = nonEmptyStrings(fields)
	if len(fields) == 0 {
		return section(text)
	}
	if len(fields) > maxSlackFields {
		hidden := len(fields) - (maxSlackFields - 1)
		fields = append(fields[:maxSlackFields-1:maxSlackFields-1], fmt.Sprintf("…and %d more", hidden))
	}
	objects := make([]slackBlock, 0, len(fields))
	for _, field := range fields {
		objects = append(objects, mrkdwn(truncateRunes(field, maxSlackFieldText)))
	}
	block := slackBlock{"type": "section", "fields": objects}
	if strings.TrimSpace(text) != "" {
		block["text"] = mrkdwn(truncateRunes(text, maxSlackSectionText))
	}
	return []slackBlock{block}
}

func contextBlocks(text string) []slackBlock {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []slackBlock{contextBlock(text)}
}

func contextBlock(text string) slackBlock {
	return slackBlock{"type": "context", "elements": []slackBlock{mrkdwn(truncateRunes(text, maxSlackSectionText))}}
}

func headerBlock(text string) slackBlock {
	return slackBlock{"type": "header", "text": slackBlock{"type": "plain_text", "text": truncateRunes(text, maxSlackHeaderText), "emoji": true}}
}

func mrkdwn(text string) slackBlock {
	return slackBlock{"type": "mrkdwn", "text": text}
}

// Text helpers. Labels and summaries are plain text and are escaped. The
// answer and the surface's markdown fields (text, callout body, step,
// event and marker details, draft body) go through markdownToSlack.

func plain(text string) string {
	return slackEscape(strings.TrimSpace(text))
}

func bold(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "*" + plain(text) + "*"
}

func italic(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return "_" + plain(text) + "_"
}

// titled puts a component's title in bold above body.
func titled(c visualComponent, body string) string {
	return joinLines(bold(c.Title), body)
}

func joinLines(lines ...string) string {
	return strings.Join(nonEmptyStrings(lines), "\n")
}

func joinWith(separator string, parts ...string) string {
	return strings.Join(nonEmptyStrings(parts), separator)
}

// listText joins list lines, keeping the first maxSlackListLines.
func listText(lines []string) string {
	if len(lines) > maxSlackListLines {
		hidden := len(lines) - maxSlackListLines
		lines = append(lines[:maxSlackListLines:maxSlackListLines], fmt.Sprintf("…and %d more", hidden))
	}
	return joinLines(lines...)
}

func plainAll(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, plain(value))
	}
	return out
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

// truncateRunes cuts text to at most limit characters, ending in "…".
func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimRightFunc(string(runes[:limit-1]), func(r rune) bool { return r == ' ' || r == '\n' }) + "…"
}

// splitSlackText splits text into chunks of at most limit characters,
// preferring line breaks.
func splitSlackText(text string, limit int) []string {
	var chunks []string
	for strings.TrimSpace(text) != "" {
		runes := []rune(text)
		if len(runes) <= limit {
			chunks = append(chunks, text)
			break
		}
		cut := limit
		if newline := strings.LastIndex(string(runes[:limit]), "\n"); newline > 0 {
			cut = utf8.RuneCountInString(string(runes[:limit])[:newline])
		}
		chunks = append(chunks, string(runes[:cut]))
		text = strings.TrimLeft(string(runes[cut:]), "\n")
	}
	return chunks
}
