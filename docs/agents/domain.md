# Domain docs

This repository uses a single-context domain-documentation layout.

## Before exploring

Read these sources when they exist and are relevant:

- `CONTEXT.md` at the repository root
- ADRs under `docs/adr/`

If they do not exist, proceed without treating their absence as a blocker. Domain-modeling workflows create them lazily when terminology or architectural decisions are resolved.

## Layout

```
/
├── CONTEXT.md
├── docs/
│   └── adr/
└── apps/
    └── cli/
```

## Vocabulary

Use the terminology defined in `CONTEXT.md` in issues, plans, tests, refactoring proposals, and implementation notes.

If a required concept is missing from the glossary, reconsider whether existing vocabulary already covers it. Otherwise, record it as a domain-modeling gap.

## ADR conflicts

If proposed work contradicts an existing ADR, surface the conflict explicitly instead of silently overriding the decision.
