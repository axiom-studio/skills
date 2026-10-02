package main

import "testing"

func TestMarkdownToSlack(t *testing.T) {
	cases := []struct{ in, want string }{
		{"**Bold** and *italic* and ~~gone~~", "*Bold* and _italic_ and ~gone~"},
		{"[Yahoo](https://finance.yahoo.com/?a=1&b=2)", "<https://finance.yahoo.com/?a=1&amp;b=2|Yahoo>"},
		{"# Heading\n\n- First\n- Second", "*Heading*\n\n• First\n• Second"},
		{"```go\nfmt.Println(\"**literal**\")\n```", "```\nfmt.Println(\"**literal**\")\n```"},
		{"Use `**literal**` and <script> & text", "Use `**literal**` and &lt;script&gt; &amp; text"},
	}
	for _, test := range cases {
		if actual := markdownToSlack(test.in); actual != test.want {
			t.Errorf("%q: got %q want %q", test.in, actual, test.want)
		}
	}
}
func TestSlackToMarkdown(t *testing.T) {
	source := "*Bold* _italic_ ~gone~ <https://example.com|Link> <@U123> <#C123|general>\n```\n*literal*\n``` and `*literal*` &amp;"
	want := "**Bold** _italic_ ~~gone~~ [Link](https://example.com) @U123 #general\n```\n*literal*\n``` and `*literal*` &"
	if actual := slackToMarkdown(source); actual != want {
		t.Fatalf("got %q want %q", actual, want)
	}
}
func TestMarkdownNeverTurnsHTMLIntoSlackMentions(t *testing.T) {
	if actual := markdownToSlack("<!here> and <@U123>"); actual != "&lt;!here&gt; and &lt;@U123&gt;" {
		t.Fatalf("unexpected mention: %s", actual)
	}
	if actual := markdownToSlack("[unsafe](javascript:alert)"); actual != "unsafe" {
		t.Fatalf("unsafe URL projected: %s", actual)
	}
}

func TestSlackCodeDelimitersStayLiteral(t *testing.T) {
	source := "Use ``*literal* `code` ~literal~`` and *bold*"
	want := "Use ``*literal* `code` ~literal~`` and **bold**"
	if got := slackToMarkdown(source); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
