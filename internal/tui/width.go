package tui

import (
	"os"
	"strconv"
	"strings"
)

const defaultRenderWidth = 80

func normalizeRenderWidth(width int) int {
	if width > 0 {
		return width
	}
	if columns := columnsWidth(); columns > 0 {
		return columns
	}
	return defaultRenderWidth
}

func columnsWidth() int {
	raw := strings.TrimSpace(os.Getenv("COLUMNS"))
	if raw == "" {
		return 0
	}
	width, err := strconv.Atoi(raw)
	if err != nil || width <= 0 {
		return 0
	}
	return width
}
