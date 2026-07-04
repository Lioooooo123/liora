package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	brandStemStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("149")).Bold(true)
	brandAccentStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Bold(true)
	brandNameStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("149")).Bold(true)
	brandAvatarStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("75")).Background(lipgloss.Color("75"))
	brandAvatarVoidStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Background(lipgloss.Color("75"))
)

func brandMark() string {
	return brandAccentStyle.Render("◐")
}

func brandInline() string {
	return brandMark() + " " + brandNameStyle.Render("LIORA")
}

func brandPrompt() string {
	return brandStemStyle.Render("› ") + brandNameStyle.Render("liora")
}

func brandAvatarLines() []string {
	fill := func(cells int) string {
		return brandAvatarStyle.Render(strings.Repeat(" ", cells))
	}
	eye := brandAvatarVoidStyle.Render(" ")
	return []string{
		fill(10),
		brandAvatarStyle.Render("   ") + eye + brandAvatarStyle.Render("  ") + eye + brandAvatarStyle.Render("   "),
		fill(10),
	}
}

func brandWelcomeLines() []string {
	return []string{
		brandPrompt(),
		mutedStyle.Render("local agent workbench"),
	}
}
