# Issue tracker: GitHub

Issues and PRDs for this repository live as GitHub Issues. Use the `gh` CLI for all operations.

## Conventions

- Create: `gh issue create --title "..." --body "..."`
- Read: `gh issue view <number> --comments`
- List: `gh issue list --state open`
- Comment: `gh issue comment <number> --body "..."`
- Add or remove labels: `gh issue edit <number> --add-label "..."` or `--remove-label "..."`
- Close: `gh issue close <number> --comment "..."`

Infer the repository from `git remote -v`; `gh` does this automatically when run inside this checkout.

## Pull requests as a triage surface

**PRs as a request surface: no.**

Pull requests are not included in the issue-triage queue unless this flag is changed to `yes`.

## Skill operations

When a skill says “publish to the issue tracker”, create a GitHub issue.

When a skill says “fetch the relevant ticket”, run:

`gh issue view <number> --comments`

## Wayfinding

A wayfinder map is one GitHub issue labelled `wayfinder:map`. Its child tickets are linked using GitHub sub-issues where available.

Child tickets use one of these labels:

- `wayfinder:research`
- `wayfinder:prototype`
- `wayfinder:grilling`
- `wayfinder:task`

Use GitHub’s native issue dependencies for blocking relationships where available. Otherwise, record dependencies as `Blocked by: #<number>` in the issue body.

Claim a ticket with:

`gh issue edit <number> --add-assignee @me`

Resolve it by adding the result as a comment, closing the issue, and updating the parent map’s decisions.
