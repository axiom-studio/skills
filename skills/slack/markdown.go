package main

import (
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"strings"

	"github.com/russross/blackfriday/v2"
)

// Canonical messages retain Markdown. Conversion happens only at Slack's edge.
func markdownToSlack(source string) string {
	var out strings.Builder
	tree := blackfriday.New(blackfriday.WithExtensions(blackfriday.CommonExtensions)).Parse([]byte(source))
	ordered := map[*blackfriday.Node]int{}
	quotes := map[*blackfriday.Node]int{}
	tree.Walk(func(n *blackfriday.Node, entering bool) blackfriday.WalkStatus {
		write := func(s string) { _, _ = io.WriteString(&out, s) }
		switch n.Type {
		case blackfriday.Text:
			if entering {
				write(slackEscape(string(n.Literal)))
			}
		case blackfriday.Emph:
			write("_")
		case blackfriday.Strong:
			write("*")
		case blackfriday.Del:
			write("~")
		case blackfriday.Code:
			if entering {
				write("`" + slackEscape(string(n.Literal)) + "`")
			}
		case blackfriday.CodeBlock:
			if entering {
				write("```\n" + slackEscape(strings.TrimRight(string(n.Literal), "\n")) + "\n```\n\n")
			}
		case blackfriday.Hardbreak:
			write("\n")
		case blackfriday.Heading:
			if entering {
				write("*")
			} else {
				write("*\n\n")
			}
		case blackfriday.Paragraph:
			if !entering {
				write("\n")
				if n.Parent == nil || n.Parent.Type != blackfriday.Item {
					write("\n")
				}
			}
		case blackfriday.List:
			if entering {
				ordered[n] = 0
			} else {
				write("\n")
			}
		case blackfriday.Item:
			if entering {
				depth := 0
				for p := n.Parent.Parent; p != nil; p = p.Parent {
					if p.Type == blackfriday.List {
						depth++
					}
				}
				write(strings.Repeat("  ", depth))
				ordered[n.Parent]++
				if n.Parent.ListFlags&blackfriday.ListTypeOrdered != 0 {
					write(fmt.Sprintf("%d. ", ordered[n.Parent]))
				} else {
					write("• ")
				}
			}
		case blackfriday.Link, blackfriday.Image:
			if entering {
				target := string(n.LinkData.Destination)
				if safeSlackLink(target) {
					write("<" + slackEscape(target) + "|")
				}
			} else if safeSlackLink(string(n.LinkData.Destination)) {
				write(">")
			}
		case blackfriday.BlockQuote:
			if entering {
				quotes[n] = out.Len()
			} else {
				content := out.String()
				start := quotes[n]
				quoted := strings.TrimRight(content[start:], "\n")
				out.Reset()
				write(content[:start] + "> " + strings.ReplaceAll(quoted, "\n", "\n> ") + "\n\n")
			}
		case blackfriday.HorizontalRule:
			if entering {
				write("—\n\n")
			}
		case blackfriday.HTMLSpan, blackfriday.HTMLBlock:
			if entering {
				write(slackEscape(string(n.Literal)))
			}
		case blackfriday.Table:
			if entering {
				write("```\n")
				n.Walk(func(cell *blackfriday.Node, enter bool) blackfriday.WalkStatus {
					if (cell.Type == blackfriday.Text || cell.Type == blackfriday.Code) && enter {
						write(slackEscape(string(cell.Literal)))
					}
					if cell.Type == blackfriday.TableCell && !enter && cell.Next != nil {
						write(" | ")
					}
					if cell.Type == blackfriday.TableRow && !enter {
						write("\n")
					}
					return blackfriday.GoToNext
				})
				write("```\n\n")
				return blackfriday.SkipChildren
			}
		}
		return blackfriday.GoToNext
	})
	return strings.TrimSpace(out.String())
}
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
func safeSlackLink(s string) bool {
	u, err := url.Parse(s)
	return err == nil && !strings.ContainsAny(s, "\r\n<>|") && ((u.Scheme == "https" || u.Scheme == "http") && u.Host != "" || u.Scheme == "mailto" && u.Opaque != "")
}

var slackReference = regexp.MustCompile(`<([^<>\n]+)>`)
var slackBold = regexp.MustCompile(`\*([^*\n]+)\*`)
var slackStrike = regexp.MustCompile(`~([^~\n]+)~`)

func slackToMarkdown(source string) string {
	// Protect fenced and inline code before interpreting formatting delimiters.
	var out strings.Builder
	for len(source) > 0 {
		start := strings.IndexByte(source, '`')
		if start < 0 {
			out.WriteString(slackTextToMarkdown(source))
			break
		}
		out.WriteString(slackTextToMarkdown(source[:start]))
		source = source[start:]
		count := 1
		for count < len(source) && source[count] == '`' {
			count++
		}
		delimiter := source[:count]
		end := strings.Index(source[len(delimiter):], delimiter)
		if end < 0 {
			out.WriteString(source)
			break
		}
		end += 2 * len(delimiter)
		out.WriteString(html.UnescapeString(source[:end]))
		source = source[end:]
	}
	return out.String()
}
func slackTextToMarkdown(s string) string {
	s = slackReference.ReplaceAllStringFunc(s, func(ref string) string {
		body := ref[1 : len(ref)-1]
		target, label, hasLabel := strings.Cut(body, "|")
		if strings.HasPrefix(target, "@") {
			return "@" + strings.TrimPrefix(target, "@")
		}
		if strings.HasPrefix(target, "#") {
			if hasLabel {
				return "#" + label
			}
			return target
		}
		if strings.HasPrefix(target, "!") {
			return "@" + strings.TrimPrefix(target, "!")
		}
		if safeSlackLink(target) {
			if hasLabel {
				return "[" + strings.NewReplacer("[", "\\[", "]", "\\]").Replace(label) + "](" + strings.ReplaceAll(target, ")", "%29") + ")"
			}
			return ref
		}
		return ref
	})
	s = doubleSlackDelimiter(s, slackBold, '*')
	s = doubleSlackDelimiter(s, slackStrike, '~')
	return html.UnescapeString(s)
}
func doubleSlackDelimiter(s string, re *regexp.Regexp, marker byte) string {
	var out strings.Builder
	last := 0
	for _, match := range re.FindAllStringIndex(s, -1) {
		out.WriteString(s[last:match[0]])
		if match[0] > 0 && s[match[0]-1] == marker || match[1] < len(s) && s[match[1]] == marker {
			out.WriteString(s[match[0]:match[1]])
		} else {
			out.WriteByte(marker)
			out.WriteString(s[match[0]:match[1]])
			out.WriteByte(marker)
		}
		last = match[1]
	}
	out.WriteString(s[last:])
	return out.String()
}
