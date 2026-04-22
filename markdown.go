package main

import (
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

var telegramMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

type telegramMarkdownRenderer struct {
	source []byte
	out    strings.Builder
	lists  []telegramListState
}

type telegramListState struct {
	ordered bool
	next    int
}

func markdownToTelegramHTML(markdown string) string {
	source := []byte(markdown)
	doc := telegramMarkdown.Parser().Parse(text.NewReader(source))

	renderer := telegramMarkdownRenderer{source: source}
	renderer.render(doc)

	return strings.TrimSpace(renderer.out.String())
}

func (r *telegramMarkdownRenderer) render(node ast.Node) {
	switch n := node.(type) {
	case *ast.Document:
		r.renderChildren(n)
	case *ast.Paragraph:
		r.renderChildren(n)
		if len(r.lists) > 0 {
			r.ensureLineBreak()
		} else {
			r.ensureBlockBreak()
		}
	case *ast.Heading:
		r.out.WriteString("<b>")
		r.renderChildren(n)
		r.out.WriteString("</b>")
		r.ensureBlockBreak()
	case *ast.TextBlock:
		r.renderChildren(n)
		if len(r.lists) > 0 {
			r.ensureLineBreak()
		} else {
			r.ensureBlockBreak()
		}
	case *ast.Text:
		writeEscapedTelegramHTML(&r.out, string(n.Value(r.source)))
		if n.HardLineBreak() || n.SoftLineBreak() {
			r.out.WriteByte('\n')
		}
	case *ast.String:
		writeEscapedTelegramHTML(&r.out, string(n.Value))
	case *ast.Emphasis:
		if n.Level >= 2 {
			r.wrap("b", n)
		} else {
			r.wrap("i", n)
		}
	case *ast.CodeSpan:
		r.wrap("code", n)
	case *ast.Link:
		r.renderLink(n)
	case *ast.Image:
		r.renderChildren(n)
	case *ast.AutoLink:
		r.renderAutoLink(n)
	case *ast.List:
		r.renderList(n)
	case *ast.ListItem:
		r.renderListItem(n)
	case *ast.Blockquote:
		r.out.WriteString("<blockquote>")
		r.renderChildren(n)
		r.trimTrailingNewlines()
		r.out.WriteString("</blockquote>")
		r.ensureBlockBreak()
	case *ast.CodeBlock:
		r.renderCodeBlock(n.Lines(), "")
	case *ast.FencedCodeBlock:
		r.renderCodeBlock(n.Lines(), string(n.Language(r.source)))
	case *ast.ThematicBreak:
		r.out.WriteString("-----")
		r.ensureBlockBreak()
	case *ast.HTMLBlock:
		r.renderSegments(n.Lines())
		r.ensureBlockBreak()
	case *ast.RawHTML:
		r.renderSegments(n.Segments)
	default:
		r.renderExtensionOrChildren(n)
	}
}

func (r *telegramMarkdownRenderer) renderChildren(node ast.Node) {
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		r.render(child)
	}
}

func (r *telegramMarkdownRenderer) renderExtensionOrChildren(node ast.Node) {
	switch node.Kind() {
	case extast.KindStrikethrough:
		r.wrap("s", node)
	case extast.KindTaskCheckBox:
		if checkbox, ok := node.(*extast.TaskCheckBox); ok && checkbox.IsChecked {
			r.out.WriteString("[x] ")
		} else {
			r.out.WriteString("[ ] ")
		}
	case extast.KindTable:
		r.renderChildren(node)
		r.ensureBlockBreak()
	case extast.KindTableRow:
		r.renderChildren(node)
		r.trimTrailingText(" | ")
		r.ensureLineBreak()
	case extast.KindTableCell, extast.KindTableHeader:
		r.renderChildren(node)
		r.out.WriteString(" | ")
	default:
		r.renderChildren(node)
	}
}

func (r *telegramMarkdownRenderer) renderLink(node *ast.Link) {
	destination := strings.TrimSpace(string(node.Destination))
	if destination == "" {
		r.renderChildren(node)
		return
	}

	r.out.WriteString(`<a href="`)
	writeEscapedTelegramHTMLAttr(&r.out, destination)
	r.out.WriteString(`">`)
	r.renderChildren(node)
	r.out.WriteString("</a>")
}

func (r *telegramMarkdownRenderer) renderAutoLink(node *ast.AutoLink) {
	destination := strings.TrimSpace(string(node.URL(r.source)))
	if destination == "" {
		destination = strings.TrimSpace(string(node.Label(r.source)))
	}
	if destination == "" {
		return
	}

	r.out.WriteString(`<a href="`)
	writeEscapedTelegramHTMLAttr(&r.out, destination)
	r.out.WriteString(`">`)
	writeEscapedTelegramHTML(&r.out, string(node.Label(r.source)))
	r.out.WriteString("</a>")
}

func (r *telegramMarkdownRenderer) renderList(node *ast.List) {
	start := node.Start
	if start == 0 {
		start = 1
	}
	r.lists = append(r.lists, telegramListState{
		ordered: node.IsOrdered(),
		next:    start,
	})
	r.renderChildren(node)
	r.lists = r.lists[:len(r.lists)-1]
	r.ensureLineBreak()
}

func (r *telegramMarkdownRenderer) renderListItem(node *ast.ListItem) {
	if len(r.lists) == 0 {
		r.renderChildren(node)
		return
	}

	state := &r.lists[len(r.lists)-1]
	indent := strings.Repeat("  ", len(r.lists)-1)
	r.out.WriteString(indent)
	if state.ordered {
		r.out.WriteString(strconv.Itoa(state.next))
		r.out.WriteString(". ")
		state.next++
	} else {
		r.out.WriteString("- ")
	}

	r.renderChildren(node)
	r.ensureLineBreak()
}

func (r *telegramMarkdownRenderer) renderCodeBlock(lines *text.Segments, language string) {
	r.out.WriteString("<pre>")
	if language != "" {
		r.out.WriteString(`<code class="language-`)
		writeEscapedTelegramHTMLAttr(&r.out, language)
		r.out.WriteString(`">`)
	} else {
		r.out.WriteString("<code>")
	}
	r.renderSegments(lines)
	r.out.WriteString("</code></pre>")
	r.ensureBlockBreak()
}

func (r *telegramMarkdownRenderer) renderSegments(segments *text.Segments) {
	for i := 0; i < segments.Len(); i++ {
		segment := segments.At(i)
		writeEscapedTelegramHTML(&r.out, string(segment.Value(r.source)))
	}
}

func (r *telegramMarkdownRenderer) wrap(tag string, node ast.Node) {
	r.out.WriteByte('<')
	r.out.WriteString(tag)
	r.out.WriteByte('>')
	r.renderChildren(node)
	r.out.WriteString("</")
	r.out.WriteString(tag)
	r.out.WriteByte('>')
}

func (r *telegramMarkdownRenderer) ensureLineBreak() {
	if r.out.Len() == 0 || strings.HasSuffix(r.out.String(), "\n") {
		return
	}
	r.out.WriteByte('\n')
}

func (r *telegramMarkdownRenderer) ensureBlockBreak() {
	r.trimTrailingNewlines()
	if r.out.Len() > 0 {
		r.out.WriteString("\n\n")
	}
}

func (r *telegramMarkdownRenderer) trimTrailingNewlines() {
	value := r.out.String()
	trimmed := strings.TrimRight(value, "\n")
	if len(trimmed) == len(value) {
		return
	}
	r.out.Reset()
	r.out.WriteString(trimmed)
}

func (r *telegramMarkdownRenderer) trimTrailingText(suffix string) {
	value := r.out.String()
	trimmed := strings.TrimSuffix(value, suffix)
	if len(trimmed) == len(value) {
		return
	}
	r.out.Reset()
	r.out.WriteString(trimmed)
}

func writeEscapedTelegramHTML(out *strings.Builder, text string) {
	for i := 0; i < len(text); i++ {
		writeEscapedTelegramHTMLByte(out, text[i])
	}
}

func writeEscapedTelegramHTMLAttr(out *strings.Builder, text string) {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '"':
			out.WriteString("&quot;")
		default:
			writeEscapedTelegramHTMLByte(out, text[i])
		}
	}
}

func writeEscapedTelegramHTMLByte(out *strings.Builder, value byte) {
	switch value {
	case '&':
		out.WriteString("&amp;")
	case '<':
		out.WriteString("&lt;")
	case '>':
		out.WriteString("&gt;")
	default:
		out.WriteByte(value)
	}
}
