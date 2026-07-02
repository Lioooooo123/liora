package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Lioooooo123/liora/internal/llm"
)

type toolFailureSignature struct {
	tool      string
	input     string
	arguments string
	errorLine string
	outputSum string
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[:index]
	}
	return text
}

func newToolFailureSignature(call llm.ToolCall, output string) toolFailureSignature {
	return toolFailureSignature{
		tool:      call.Name,
		input:     toolInput(call),
		arguments: canonicalToolArguments(call.Arguments),
		errorLine: firstLine(output),
		outputSum: outputDigest(output),
	}
}

func outputDigest(output string) string {
	sum := sha256.Sum256([]byte(output))
	return fmt.Sprintf("%x", sum)
}

func canonicalToolArguments(raw string) string {
	args, err := parseToolArgs(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	data, err := json.Marshal(args)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return string(data)
}

func renderRepeatedFailure(signature toolFailureSignature) string {
	target := signature.input
	if target == "" {
		target = signature.arguments
	}
	return fmt.Sprintf("stopped after repeated failing tool call: %s %s: %s", signature.tool, target, signature.errorLine)
}
