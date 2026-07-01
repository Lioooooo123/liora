package tui

import "github.com/charmbracelet/lipgloss"

var (
	brandMarkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("149")).Bold(true)
	brandNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true)
)

func brandInline() string {
	return brandMarkStyle.Render("✦") + " " + brandNameStyle.Render("LIORA")
}

func brandWelcomeLines() []string {
	return []string{
		brandInline(),
		mutedStyle.Render("local agent workbench"),
	}
}
