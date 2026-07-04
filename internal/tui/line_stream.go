package tui

import (
	"fmt"
	"io"
	"strings"
)

type lineStreamRenderer struct {
	output             io.Writer
	width              int
	assistantBuffer    strings.Builder
	assistantText      strings.Builder
	assistantPanelOpen bool
	assistantCodeBlock bool
}

func newLineStreamRenderer(output io.Writer, widths ...int) *lineStreamRenderer {
	width := 0
	if len(widths) > 0 {
		width = widths[0]
	}
	return &lineStreamRenderer{output: output, width: normalizeRenderWidth(width)}
}

func (l *streamingLoop) renderStreamUpdate(update StreamUpdate) {
	if l.renderer == nil {
		l.renderer = newLineStreamRenderer(l.output)
	}
	l.renderer.Render(update)
}

func (l *streamingLoop) flushStream() {
	if l.renderer == nil {
		return
	}
	l.renderer.Flush()
	l.renderer = nil
}

func (r *lineStreamRenderer) Render(update StreamUpdate) {
	section := FormatDaemonEventUpdate(update)
	if update.Type == daemonEventAssistantDelta && section.Visible && section.Title == "Assistant" {
		r.renderAssistantDelta(section.Body)
		return
	}
	if update.Type == daemonEventSummary && section.Visible && section.Title == "Assistant" {
		if r.hasPendingAssistant() {
			r.finalizeAssistantSummary(section.Body)
			return
		}
		renderSectionWithWidth(r.output, section.Title, section.Body, r.width)
		return
	}
	r.Flush()
	if section.Visible {
		renderSectionWithWidth(r.output, section.Title, section.Body, r.width)
	}
}

func (r *lineStreamRenderer) Flush() {
	if !r.hasPendingAssistant() {
		return
	}
	if r.assistantBuffer.Len() > 0 {
		r.renderAssistantLine(r.assistantBuffer.String())
		r.assistantBuffer.Reset()
	}
	r.closeAssistantPanel()
	r.assistantText.Reset()
}

func (r *lineStreamRenderer) hasPendingAssistant() bool {
	return r.assistantPanelOpen || r.assistantBuffer.Len() > 0
}

func (r *lineStreamRenderer) renderAssistantDelta(delta string) {
	r.assistantText.WriteString(delta)
	r.assistantBuffer.WriteString(delta)
	for {
		body := r.assistantBuffer.String()
		idx := strings.IndexByte(body, '\n')
		if idx < 0 {
			return
		}
		line := body[:idx]
		r.assistantBuffer.Reset()
		r.assistantBuffer.WriteString(body[idx+1:])
		r.renderAssistantLine(line)
	}
}

func (r *lineStreamRenderer) finalizeAssistantSummary(summary string) {
	current := r.assistantText.String()
	if strings.HasPrefix(summary, current) {
		r.renderAssistantDelta(summary[len(current):])
		r.Flush()
		return
	}
	r.Flush()
	if strings.TrimSpace(summary) != "" && strings.TrimSpace(summary) != strings.TrimSpace(current) {
		renderSectionWithWidth(r.output, "Assistant", summary, 0)
	}
}

func (r *lineStreamRenderer) renderAssistantLine(line string) {
	if isMarkdownFenceLine(line) {
		r.assistantCodeBlock = !r.assistantCodeBlock
		return
	}
	if r.assistantCodeBlock {
		r.writeAssistantPanelLine(line)
		return
	}
	rendered, ok := renderSectionMarkdown("Assistant", line, r.bodyWidth())
	if !ok {
		rendered = line
	} else {
		rendered = strings.TrimSpace(rendered)
	}
	rendered = wrapSectionBody(rendered, r.bodyWidth())
	for _, renderedLine := range strings.Split(rendered, "\n") {
		r.writeAssistantPanelLine(renderedLine)
	}
}

func (r *lineStreamRenderer) writeAssistantPanelLine(line string) {
	r.openAssistantPanel()
	if r.bodyWidth() > 0 {
		line = truncateCells(line, r.bodyWidth())
	}
	if line == "" {
		fmt.Fprintln(r.output, railStyle.Render("│"))
		return
	}
	fmt.Fprintln(r.output, railStyle.Render("│ ")+line)
}

func (r *lineStreamRenderer) openAssistantPanel() {
	if r.assistantPanelOpen {
		return
	}
	fmt.Fprintln(r.output, "\n"+railStyle.Render("╭─ ")+labelStyle.Render("Assistant"))
	r.assistantPanelOpen = true
}

func (r *lineStreamRenderer) closeAssistantPanel() {
	if !r.assistantPanelOpen {
		return
	}
	fmt.Fprintln(r.output, railStyle.Render("╰"))
	r.assistantPanelOpen = false
	r.assistantCodeBlock = false
}

func (r *lineStreamRenderer) bodyWidth() int {
	if r.width <= 2 {
		return 0
	}
	return r.width - 2
}

func isMarkdownFenceLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}
