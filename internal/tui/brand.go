package tui

import "github.com/charmbracelet/lipgloss"

var (
	brandStemStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true)
	brandAccentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	brandNameStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Bold(true)
)

func brandMark() string {
	return brandStemStyle.Render("▌") + brandAccentStyle.Render("●")
}

func brandInline() string {
	return brandMark() + " " + brandNameStyle.Render("LIORA")
}

func brandWelcomeLines() []string {
	return []string{
		brandInline(),
		mutedStyle.Render("local agent workbench"),
	}
}
