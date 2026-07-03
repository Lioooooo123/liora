package tui

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Lioooooo123/liora/internal/trace"
	"github.com/charmbracelet/lipgloss"
)

func RenderStreamUpdate(output io.Writer, update StreamUpdate) {
	RenderStreamUpdateWithWidth(output, update, 0)
}

func RenderStreamUpdateWithWidth(output io.Writer, update StreamUpdate, width int) {
	section := FormatDaemonEventUpdate(update)
	if section.Visible {
		renderSectionWithWidth(output, section.Title, section.Body, width)
	}
}

func RenderTurn(output io.Writer, view TurnView) {
	result := view.TurnResult
	if view.ShowUser {
		renderSection(output, "You", view.Input)
	}
	if strings.TrimSpace(result.Answer) != "" {
		renderSection(output, "Assistant", result.Answer)
	}
	if strings.TrimSpace(result.AgentResult.Summary) != "" && strings.TrimSpace(result.Answer) == "" {
		renderSection(output, "Assistant", result.AgentResult.Summary)
	}
	if strings.TrimSpace(result.AgentResult.Diff) != "" {
		renderSection(output, "Assistant", PatchReadyReply(result.AgentResult.Diff))
	}
}

func formatPlan(steps string) string {
	var lines []string
	for _, step := range strings.Split(steps, "\n") {
		if strings.TrimSpace(step) != "" {
			lines = append(lines, "- "+step)
		}
	}
	return strings.Join(lines, "\n")
}

func formatToolEvent(payload eventPayload) string {
	status := renderStatus(payload.Status)
	var lines []string
	lines = append(lines, status+" "+metadataStyle.Render(strings.TrimSpace(payload.Tool+" "+payload.Input)))
	out := strings.TrimSpace(payload.Output)
	if out != "" {
		for _, line := range formatToolOutput(out, 12) {
			lines = append(lines, "  "+line)
		}
	}
	return strings.Join(lines, "\n")
}

func renderSection(output io.Writer, title string, body string) {
	renderSectionWithWidth(output, title, body, 0)
}

func renderSectionWithWidth(output io.Writer, title string, body string, width int) {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return
	}
	bodyWidth := 0
	if width > 2 {
		bodyWidth = width - 2
	}
	var renderedMarkdown bool
	body, renderedMarkdown = renderSectionMarkdown(title, body, bodyWidth)
	if bodyWidth > 0 && !renderedMarkdown {
		body = wrapSectionBody(body, bodyWidth)
	}
	fmt.Fprintln(output, "\n"+renderPanel(title, strings.Split(indentBody(body), "\n")))
}

func wrapSectionBody(body string, width int) string {
	if width <= 0 {
		return body
	}
	return lipgloss.NewStyle().Width(width).Render(body)
}

func renderLogLine(output io.Writer, label string, body string) {
	body = firstNonEmptyLine(strings.TrimSpace(body))
	if body == "" {
		return
	}
	label = strings.TrimSpace(label)
	if label != "" {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	fmt.Fprintln(output, mutedStyle.Render("  "+label+" - ")+body)
}

func keyValue(key string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "-"
	}
	return labelStyle.Render(key) + " " + metadataStyle.Render(value)
}

func renderStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" || status == string(trace.StatusOK) {
		return okStyle.Render("ok")
	}
	if status == "cancelled" || status == "waiting_user" {
		return warnStyle.Render(status)
	}
	return errStyle.Render(status)
}

func indentBody(body string) string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func renderPanel(title string, lines []string) string {
	var rendered []string
	rendered = append(rendered, railStyle.Render("╭─ ")+labelStyle.Render(title))
	for _, line := range lines {
		if line == "" {
			rendered = append(rendered, railStyle.Render("│"))
			continue
		}
		rendered = append(rendered, railStyle.Render("│ ")+line)
	}
	rendered = append(rendered, railStyle.Render("╰"))
	return strings.Join(rendered, "\n")
}

func firstNonEmptyLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func formatToolOutput(value string, maxLines int) []string {
	var lines []string
	rawLines := strings.Split(strings.TrimSpace(value), "\n")
	for i, line := range rawLines {
		if i >= maxLines {
			remaining := len(rawLines) - maxLines
			lines = append(lines, "... "+strconv.Itoa(remaining)+" more lines")
			break
		}
		line = strings.TrimRight(line, "\r")
		if len(line) > 140 {
			line = line[:137] + "..."
		}
		lines = append(lines, line)
	}
	return lines
}
