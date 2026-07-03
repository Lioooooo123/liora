package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Lioooooo123/liora/internal/capabilities"
	"github.com/Lioooooo123/liora/internal/llm"
)

const workspaceResource = "workspace"

type toolAccess struct {
	mode     capabilities.ToolAccessMode
	resource string
}

type scheduledToolCall struct {
	index int
	call  llm.ToolCall
}

type toolBatch []scheduledToolCall

func scheduleToolBatches(calls []llm.ToolCall) []toolBatch {
	if len(calls) == 0 {
		return nil
	}
	var batches []toolBatch
	var current toolBatch
	for index, call := range calls {
		next := scheduledToolCall{index: index, call: call}
		if len(current) > 0 && batchConflicts(current, next) {
			batches = append(batches, current)
			current = nil
		}
		current = append(current, next)
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

func batchConflicts(batch toolBatch, next scheduledToolCall) bool {
	nextAccesses := toolAccesses(next.call)
	for _, scheduled := range batch {
		if accessesConflict(toolAccesses(scheduled.call), nextAccesses) {
			return true
		}
	}
	return false
}

func accessesConflict(left []toolAccess, right []toolAccess) bool {
	for _, l := range left {
		for _, r := range right {
			if accessConflict(l, r) {
				return true
			}
		}
	}
	return false
}

func accessConflict(left toolAccess, right toolAccess) bool {
	if left.mode == capabilities.ToolAccessExclusive || right.mode == capabilities.ToolAccessExclusive {
		return true
	}
	if left.mode == capabilities.ToolAccessWrite && right.mode == capabilities.ToolAccessWrite {
		return true
	}
	if left.resource == workspaceResource || right.resource == workspaceResource {
		return left.mode == capabilities.ToolAccessWrite || right.mode == capabilities.ToolAccessWrite
	}
	if !resourcesOverlap(left.resource, right.resource) {
		return false
	}
	return left.mode == capabilities.ToolAccessWrite || right.mode == capabilities.ToolAccessWrite
}

func resourcesOverlap(left string, right string) bool {
	if left == right {
		return true
	}
	leftPath, ok := strings.CutPrefix(left, "path:")
	if !ok {
		return false
	}
	rightPath, ok := strings.CutPrefix(right, "path:")
	if !ok {
		return false
	}
	return pathContains(leftPath, rightPath) || pathContains(rightPath, leftPath)
}

func pathContains(parent string, child string) bool {
	parent = strings.TrimSuffix(parent, "/")
	child = strings.TrimSuffix(child, "/")
	if parent == "." || parent == "" {
		return true
	}
	return strings.HasPrefix(child, parent+"/")
}

func toolAccesses(call llm.ToolCall) []toolAccess {
	args, err := parseToolArgs(call.Arguments)
	if err != nil {
		return []toolAccess{exclusiveWorkspaceAccess()}
	}
	spec, ok := capabilities.ToolAccessFor(call.Name)
	if !ok {
		return []toolAccess{exclusiveWorkspaceAccess()}
	}
	return []toolAccess{accessForSpec(spec, args)}
}

func accessForSpec(spec capabilities.ToolAccessSpec, args map[string]any) toolAccess {
	switch spec.Resource {
	case capabilities.ToolAccessPath:
		return toolAccess{mode: spec.Mode, resource: pathResource(argStringOr(args, spec.Argument, spec.Default))}
	case capabilities.ToolAccessWorkspace:
		return toolAccess{mode: spec.Mode, resource: workspaceResource}
	case capabilities.ToolAccessTodo:
		return toolAccess{mode: spec.Mode, resource: "todo"}
	case capabilities.ToolAccessSkill:
		return toolAccess{mode: spec.Mode, resource: "skill:" + argString(args, spec.Argument)}
	case capabilities.ToolAccessTask:
		return toolAccess{mode: spec.Mode, resource: "task:" + argString(args, spec.Argument)}
	default:
		return exclusiveWorkspaceAccess()
	}
}

func exclusiveWorkspaceAccess() toolAccess {
	return toolAccess{mode: capabilities.ToolAccessExclusive, resource: workspaceResource}
}

func pathResource(path string) string {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if path == "" {
		path = "."
	}
	return "path:" + path
}

func (l *ToolLoop) dispatchBatch(ctx context.Context, batch toolBatch, outcomes []toolOutcome) {
	if len(batch) == 1 {
		scheduled := batch[0]
		outcomes[scheduled.index] = l.dispatchOne(ctx, scheduled.call)
		return
	}
	sem := make(chan struct{}, maxReadOnlyConcurrency)
	var wg sync.WaitGroup
	for _, scheduled := range batch {
		wg.Add(1)
		sem <- struct{}{}
		go func(scheduled scheduledToolCall) {
			defer wg.Done()
			defer func() { <-sem }()
			outcomes[scheduled.index] = l.dispatchOne(ctx, scheduled.call)
		}(scheduled)
	}
	wg.Wait()
}
