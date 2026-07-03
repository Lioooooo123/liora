package tui

import (
	"io"
	"strings"
)

type lineStreamRenderer struct {
	output          io.Writer
	assistantBuffer strings.Builder
	hasAssistant    bool
}

func newLineStreamRenderer(output io.Writer) *lineStreamRenderer {
	return &lineStreamRenderer{output: output}
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
		r.hasAssistant = true
		r.assistantBuffer.WriteString(section.Body)
		return
	}
	if update.Type == daemonEventSummary && section.Visible && section.Title == "Assistant" {
		r.clearAssistant()
		renderSectionWithWidth(r.output, section.Title, section.Body, 0)
		return
	}
	r.Flush()
	if section.Visible {
		renderSectionWithWidth(r.output, section.Title, section.Body, 0)
	}
}

func (r *lineStreamRenderer) Flush() {
	if !r.hasAssistant {
		return
	}
	body := r.assistantBuffer.String()
	r.clearAssistant()
	renderSectionWithWidth(r.output, "Assistant", body, 0)
}

func (r *lineStreamRenderer) clearAssistant() {
	r.assistantBuffer.Reset()
	r.hasAssistant = false
}
