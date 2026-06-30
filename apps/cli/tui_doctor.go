package main

import (
	"context"
	"strings"

	"github.com/Lioooooo123/liora/internal/llm"
)

type doctorCommand struct {
	config llm.Config
	report doctorReportContext
}

func (c doctorCommand) HandleCommand(_ context.Context, line string) (string, bool, error) {
	switch strings.TrimSpace(line) {
	case "/doctor", "/config":
		report, err := doctorReport(c.config, c.report)
		return report, true, err
	default:
		return "", false, nil
	}
}
