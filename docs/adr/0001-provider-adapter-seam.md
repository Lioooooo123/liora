# ADR 0001: Provider Adapter Seam

## Status

Accepted.

## Context

Provider knowledge previously appeared in separate switches for plain generation, streaming, tool calls, capabilities, defaults and CLI configuration. A provider protocol change therefore required coordinated edits across unrelated callers, and Codex-specific OAuth and Responses continuation behavior was especially easy to lose.

## Decision

`internal/llm` exposes one deep provider seam: an adapter completes one model turn from a normalized request and returns a normalized completion. Each built-in provider owns its aliases, display name, authentication mode, defaults, capabilities and adapter construction in its provider definition.

Plain generation, streaming generation, tool generation and streaming tool generation all cross the same seam. Provider-specific continuation data is carried as opaque `ProviderState`; only the adapter that produced it interprets it.

OpenAI-compatible providers may share protocol implementation internally, but they retain separate provider definitions. Shared HTTP, SSE, retry and metrics code remains transport infrastructure rather than provider policy.

## Consequences

- Adding a built-in provider requires one adapter and one registry entry, without changing Agent or Planner control flow.
- Provider request and response regressions are tested through the common client interface and provider-specific HTTP fixtures.
- Codex can preserve encrypted reasoning items and raw call identifiers without exposing their schema to the Agent loop.
- Authentication commands may remain provider-specific user flows, but request routing and credential requirements come from provider definitions.
