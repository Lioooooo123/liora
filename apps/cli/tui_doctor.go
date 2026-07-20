package main

import (
	"context"
	"strings"

	"github.com/Lioooooo123/liora/internal/llm"
)

type doctorCommand struct {
	config llm.Config
	report doctorReportContext
	auth   codexAuthenticator
}

func (c doctorCommand) HandleCommand(_ context.Context, line string) (string, bool, error) {
	switch strings.TrimSpace(line) {
	case "/doctor", "/config":
		reportContext := c.report
		if c.auth != nil {
			status, err := c.auth.Status("openai-codex")
			reportContext.CodexAuth = codexAuthReport(status, err)
		}
		report, err := doctorReport(c.config, reportContext)
		return report, true, err
	default:
		return "", false, nil
	}
}
