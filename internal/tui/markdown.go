package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

const defaultMarkdownWidth = 80

func renderSectionMarkdown(title string, body string, width int) (string, bool) {
	if strings.TrimSpace(title) != "Assistant" {
		return body, false
	}
	if !looksLikeMarkdown(body) {
		return body, false
	}
	rendered, err := renderMarkdown(body, width)
	if err != nil {
		return body, false
	}
	return rendered, true
}

func renderMarkdown(body string, width int) (string, error) {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(lioraMarkdownStyle()),
		glamour.WithWordWrap(markdownWidth(width)),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return "", err
	}
	rendered, err := renderer.Render(body)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(rendered, "\n"), nil
}

func markdownWidth(width int) int {
	if width > 0 {
		return width
	}
	return defaultMarkdownWidth
}

func looksLikeMarkdown(body string) bool {
	if strings.Contains(body, "```") ||
		strings.Contains(body, "~~~") ||
		strings.Contains(body, "**") ||
		strings.Contains(body, "__") ||
		strings.Contains(body, "`") ||
		strings.Contains(body, "](") {
		return true
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") ||
			strings.HasPrefix(line, "## ") ||
			strings.HasPrefix(line, "### ") ||
			strings.HasPrefix(line, "#### ") ||
			strings.HasPrefix(line, "##### ") ||
			strings.HasPrefix(line, "###### ") ||
			strings.HasPrefix(line, "- ") ||
			strings.HasPrefix(line, "* ") ||
			strings.HasPrefix(line, "+ ") ||
			strings.HasPrefix(line, "> ") {
			return true
		}
	}
	return false
}

func lioraMarkdownStyle() ansi.StyleConfig {
	config := styles.DarkStyleConfig
	zero := uint(0)
	config.Document.Margin = &zero
	config.Document.BlockPrefix = ""
	config.Document.BlockSuffix = ""
	clearStyleColors(&config.Document.StylePrimitive)
	clearStyleColors(&config.Text)
	clearStyleColors(&config.Paragraph.StylePrimitive)
	clearStyleColors(&config.BlockQuote.StylePrimitive)
	clearStyleColors(&config.List.StylePrimitive)
	clearStyleColors(&config.Heading.StylePrimitive)
	clearStyleColors(&config.H1.StylePrimitive)
	clearStyleColors(&config.H2.StylePrimitive)
	clearStyleColors(&config.H3.StylePrimitive)
	clearStyleColors(&config.H4.StylePrimitive)
	clearStyleColors(&config.H5.StylePrimitive)
	clearStyleColors(&config.H6.StylePrimitive)
	clearStyleColors(&config.Item)
	clearStyleColors(&config.Enumeration)
	clearStyleColors(&config.Task.StylePrimitive)
	clearStyleColors(&config.Link)
	clearStyleColors(&config.LinkText)
	clearStyleColors(&config.Code.StylePrimitive)
	clearStyleColors(&config.CodeBlock.StylePrimitive)
	clearStyleColors(&config.Table.StylePrimitive)
	clearStyleColors(&config.DefinitionList.StylePrimitive)
	clearStyleColors(&config.DefinitionTerm)
	clearStyleColors(&config.DefinitionDescription)
	clearStyleColors(&config.HTMLBlock.StylePrimitive)
	clearStyleColors(&config.HTMLSpan.StylePrimitive)
	config.CodeBlock.Margin = &zero
	config.CodeBlock.Chroma = nil
	config.H1.Prefix = ""
	config.H1.Suffix = ""
	config.H1.BackgroundColor = nil
	config.H2.Prefix = ""
	config.H3.Prefix = ""
	config.H4.Prefix = ""
	config.H5.Prefix = ""
	config.H6.Prefix = ""
	return config
}

func clearStyleColors(style *ansi.StylePrimitive) {
	style.Color = nil
	style.BackgroundColor = nil
}
