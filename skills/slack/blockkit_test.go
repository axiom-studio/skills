package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func decodeTestComponent(t *testing.T, raw string) visualComponent {
	t.Helper()
	var component visualComponent
	if err := json.Unmarshal([]byte(raw), &component); err != nil {
		t.Fatal(err)
	}
	return component
}

func blocksJSON(t *testing.T, blocks []slackBlock) string {
	t.Helper()
	encoded, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// Golden blocks for one component of each family: prose, callout, fields,
// table, lists, progress, static interactive types, images, data visuals
// as summary and fields, flattened layouts and unknown types.
func TestBlockKitRendersComponentFamilies(t *testing.T) {
	for _, test := range []struct{ name, component, want string }{
		{name: "text", component: `{"id":"t","type":"text","title":"Why","text":"Revenue **grew** <fast> & well."}`,
			want: `[{"text":{"text":"*Why*\nRevenue *grew* \u0026lt;fast\u0026gt; \u0026amp; well.","type":"mrkdwn"},"type":"section"}]`},
		{name: "caption", component: `{"id":"c","type":"caption","text":"Source: _finance_"}`,
			want: `[{"elements":[{"text":"Source: _finance_","type":"mrkdwn"}],"type":"context"}]`},
		{name: "divider", component: `{"id":"d","type":"divider","text":"Details"}`,
			want: `[{"type":"divider"},{"elements":[{"text":"Details","type":"mrkdwn"}],"type":"context"}]`},
		{name: "callout", component: `{"id":"k","type":"callout","tone":"warning","summary":"Heads up","callout":{"body":"Check **invoices**."}}`,
			want: `[{"text":{"text":":warning: Check *invoices*.","type":"mrkdwn"},"type":"section"}]`},
		{name: "stat_row", component: `{"id":"s","type":"stat_row","summary":"Totals","stat_row":{"stats":[{"label":"Revenue","value":96000,"format":"currency","currency":"GBP","delta":"+12%","trend":"up"},{"label":"Calls","value":3900,"format":"duration"}]}}`,
			want: `[{"fields":[{"text":"*Revenue*\nGBP 96,000 (+12%) ↑","type":"mrkdwn"},{"text":"*Calls*\n1h 5m","type":"mrkdwn"}],"type":"section"}]`},
		{name: "table", component: `{"id":"tb","type":"table","title":"Regions","summary":"By region","table":{"columns":[{"key":"r","label":"Region","kind":"text"},{"key":"v","label":"Revenue","kind":"currency","currency":"USD"}],"rows":[{"r":"East","v":1200.5},{"r":"West","v":800}]}}`,
			want: `[{"text":{"text":"*Regions*","type":"mrkdwn"},"type":"section"},{"column_settings":[{"align":"left","is_wrapped":true},{"align":"right","is_wrapped":true}],"rows":[[{"text":"Region","type":"raw_text"},{"text":"Revenue","type":"raw_text"}],[{"text":"East","type":"raw_text"},{"text":"USD 1,200.5","type":"raw_text"}],[{"text":"West","type":"raw_text"},{"text":"USD 800","type":"raw_text"}]],"type":"table"}]`},
		{name: "steps", component: `{"id":"st","type":"steps","summary":"How","steps":{"items":[{"id":"a","title":"Open","detail":"the app"},{"id":"b","title":"Pay"}]}}`,
			want: `[{"text":{"text":"1. *Open* — the app\n2. *Pay*","type":"mrkdwn"},"type":"section"}]`},
		{name: "progress", component: `{"id":"p","type":"progress","summary":"Goals","progress":{"kind":"bar","items":[{"id":"a","label":"Savings","value":600,"target":1000,"format":"currency","currency":"EUR"}]}}`,
			want: "[{\"text\":{\"text\":\"*Savings*\\n`▓▓▓▓▓▓░░░░ 60%` EUR 600 of EUR 1,000\",\"type\":\"mrkdwn\"},\"type\":\"section\"}]"},
		{name: "choices", component: `{"id":"ch","type":"choices","summary":"Next","choices":{"chips":[{"id":"a","label":"Dig in"},{"id":"b","label":"Plan Q3"}]}}`,
			want: `[{"text":{"text":"Next\n• Dig in\n• Plan Q3","type":"mrkdwn"},"type":"section"}]`},
		{name: "quiz", component: `{"id":"q","type":"quiz","summary":"Check","quiz":{"question":"Which grew?","options":[{"id":"e","label":"East"},{"id":"w","label":"West"}]}}`,
			want: `[{"text":{"text":"*Which grew?*\n• East\n• West","type":"mrkdwn"},"type":"section"}]`},
		{name: "image", component: `{"id":"i","type":"image","summary":"A chart photo","image":{"image":{"artifactId":"x","version":1},"caption":"Q3 board"}}`,
			want: `[{"elements":[{"text":":frame_with_picture: Q3 board","type":"mrkdwn"}],"type":"context"}]`},
		{name: "chart", component: `{"id":"g","type":"chart","summary":"Revenue rose each month","chart":{"kind":"bar","format":"percent","series":[{"name":"Growth","points":[{"label":"Jan","value":3},{"label":"Feb","value":5.5}]}]}}`,
			want: `[{"fields":[{"text":"*Jan*\n3%","type":"mrkdwn"},{"text":"*Feb*\n5.5%","type":"mrkdwn"}],"text":{"text":"Revenue rose each month","type":"mrkdwn"},"type":"section"}]`},
		{name: "tabs", component: `{"id":"tabs","type":"tabs","tabs":[{"label":"Simple","children":[{"id":"x","type":"text","text":"Short."}]},{"label":"Detailed","children":[{"id":"y","type":"text","text":"Long."}]}]}`,
			want: `[{"elements":[{"text":"*Simple*","type":"mrkdwn"}],"type":"context"},{"text":{"text":"Short.","type":"mrkdwn"},"type":"section"},{"elements":[{"text":"*Detailed*","type":"mrkdwn"}],"type":"context"},{"text":{"text":"Long.","type":"mrkdwn"},"type":"section"}]`},
		{name: "unknown", component: `{"id":"u","type":"hologram","summary":"Something new","hologram":{"x":1}}`,
			want: `[{"text":{"text":"Something new","type":"mrkdwn"},"type":"section"}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := blocksJSON(t, (&blockRenderer{}).component(decodeTestComponent(t, test.component)))
			if got != test.want {
				t.Fatalf("blocks =\n%s\nwant\n%s", got, test.want)
			}
		})
	}
}

func TestBlockKitDrawsOneTableBlockPerMessage(t *testing.T) {
	table := decodeTestComponent(t, `{"id":"t","type":"table","summary":"T","table":{"columns":[{"key":"a","label":"A","kind":"text"}],"rows":[{"a":"x<y"}]}}`)
	renderer := &blockRenderer{}
	first, second := renderer.component(table), renderer.component(table)
	if len(first) != 1 || first[0]["type"] != "table" {
		t.Fatalf("first = %s", blocksJSON(t, first))
	}
	if got := blocksJSON(t, second); got != `[{"text":{"text":"`+"```"+`\nA\nx\u0026lt;y\n`+"```"+`","type":"mrkdwn"},"type":"section"}]` {
		t.Fatalf("second = %s", got)
	}
}

func TestBlockKitKeepsSlackLimits(t *testing.T) {
	long := strings.Repeat("word ", 1000)
	section := (&blockRenderer{}).component(decodeTestComponent(t, fmt.Sprintf(`{"id":"t","type":"text","text":%q}`, long)))
	if text := section[0]["text"].(slackBlock)["text"].(string); utf8.RuneCountInString(text) != maxSlackSectionText || !strings.HasSuffix(text, "…") {
		t.Fatalf("section runes = %d", utf8.RuneCountInString(text))
	}

	var points []string
	for index := 0; index < 15; index++ {
		points = append(points, fmt.Sprintf(`{"label":"P%d","value":%d}`, index, index))
	}
	chart := (&blockRenderer{}).component(decodeTestComponent(t, `{"id":"c","type":"chart","summary":"S","chart":{"kind":"line","series":[{"name":"s","points":[`+strings.Join(points, ",")+`]}]}}`))
	fields := chart[0]["fields"].([]slackBlock)
	if len(fields) != maxSlackFields || fields[9]["text"] != "…and 6 more" {
		t.Fatalf("fields = %s", blocksJSON(t, chart))
	}

	var rows []string
	for index := 0; index < 150; index++ {
		rows = append(rows, fmt.Sprintf(`{"a":"row %d"}`, index))
	}
	table := (&blockRenderer{}).component(decodeTestComponent(t, `{"id":"t","type":"table","summary":"S","table":{"columns":[{"key":"a","label":"A","kind":"text"}],"rows":[`+strings.Join(rows, ",")+`]}}`))
	if len(table) != 2 || len(table[0]["rows"].([][]slackBlock)) != maxSlackTableRows || table[1]["elements"].([]slackBlock)[0]["text"] != "…and 51 more rows" {
		t.Fatalf("table = %d blocks", len(table))
	}

	var components []string
	for index := 0; index < 60; index++ {
		components = append(components, fmt.Sprintf(`{"id":"t%d","type":"text","text":"Part %d"}`, index, index))
	}
	visual := decodeTestVisual(t, `{"artifactId":"v","version":1,"fallbackText":"**Long**","surface":{"title":"`+strings.Repeat("T", 200)+`","components":[`+strings.Join(components, ",")+`]}}`)
	blocks, text := slackVisualMessage(strings.Repeat("a", 3500)+"\n"+strings.Repeat("b", 10), []conversationVisual{visual}, "")
	if len(blocks) != maxSlackBlocks || blocks[0]["type"] != "section" || blocks[1]["type"] != "section" ||
		blocks[maxSlackBlocks-1]["elements"].([]slackBlock)[0]["text"] != slackFullCardNotice {
		t.Fatalf("blocks = %d, last = %v", len(blocks), blocks[len(blocks)-1])
	}
	if header := blocks[2]["text"].(slackBlock)["text"].(string); blocks[2]["type"] != "header" || utf8.RuneCountInString(header) != maxSlackHeaderText {
		t.Fatalf("header = %v", blocks[2])
	}
	if utf8.RuneCountInString(text) > maxSlackMessageText || !strings.HasPrefix(text, "aaa") {
		t.Fatalf("text runes = %d", utf8.RuneCountInString(text))
	}
}

func TestBlockKitAnswerTextSplitsAtLineBreaks(t *testing.T) {
	chunks := splitSlackText(strings.Repeat("x", 10)+"\n"+strings.Repeat("y", 10), 15)
	if len(chunks) != 2 || chunks[0] != strings.Repeat("x", 10) || chunks[1] != strings.Repeat("y", 10) {
		t.Fatalf("chunks = %q", chunks)
	}
}

func decodeTestVisual(t *testing.T, raw string) conversationVisual {
	t.Helper()
	var visual conversationVisual
	if err := json.Unmarshal([]byte(raw), &visual); err != nil {
		t.Fatal(err)
	}
	return visual
}

// testVisualJSON is the wire form the host sends for one visual.
const testVisualJSON = `{"artifactId":"visual-1","version":2,"fallbackText":"**Quarter review**\nNext\n- Dig in","futureField":true,` +
	`"surface":{"schema":"openseal.dev/presentation/v2","title":"Quarter review","state":"ready","components":[` +
	`{"id":"ch","type":"choices","summary":"Next","choices":{"chips":[{"id":"a","label":"Dig in"}]}}]}}`

func TestBlockKitMessageLeadsWithTheAnswer(t *testing.T) {
	blocks, text := slackVisualMessage("Revenue **grew**.", []conversationVisual{decodeTestVisual(t, testVisualJSON)}, "")
	want := `[{"text":{"text":"Revenue *grew*.","type":"mrkdwn"},"type":"section"},` +
		`{"text":{"emoji":true,"text":"Quarter review","type":"plain_text"},"type":"header"},` +
		`{"text":{"text":"Next\n• Dig in","type":"mrkdwn"},"type":"section"}]`
	if got := blocksJSON(t, blocks); got != want {
		t.Fatalf("blocks =\n%s\nwant\n%s", got, want)
	}
	if text != "Revenue *grew*.\n\n*Quarter review*\nNext\n- Dig in" {
		t.Fatalf("text = %q", text)
	}
}
