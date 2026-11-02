## Agent skills

### Issue tracker

Issues and specs live as local markdown under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: root `GLOSSARY.md` plus `docs/adr/`. See `docs/agents/domain.md`.

## sfdb

Standalone Go CLI that clones Salesforce objects into SQLite.

- Build: `go build -o bin/sfdb ./cmd/sfdb`
- Test: `go test ./...`; vet with `go vet ./...`
- Package direction: `cli -> pipeline -> schema`; `salesforce` and `store` are adapters around it.
- Research notes and the ADR live under `docs/` (gitignored in this checkout).

## Git standards

### Branches

- Work on a topic branch. Do not commit directly to the integration branch.
- Name branches `<type>/<short-kebab-topic>`, for example `feat/object-selectors` or
  `fix/stale-token-retry`.
- Keep one branch focused on one feature or fix. Rebase on `main` before opening a PR.
- Do not force-push a shared branch. Rebasing or force-pushing your own topic branch is fine, but
  coordinate first if anyone else is working on it.

### Commits

Use Conventional Commits:

```text
<type>(<scope>): <summary>

<body>
```

- `type` is one of `feat`, `fix`, `test`, `docs`, `refactor`, `perf`, or `chore`.
- `scope` is optional. Use it when the area is not obvious: `cli`, `pipeline`, `salesforce`,
  `store`, `schema`, `selection`, or `docs`.
- Write the summary in the imperative mood, lowercase after the colon, and without a trailing
  period. Keep it at 72 characters or fewer.
- Add a body when the reason for the change is not obvious from the summary. Separate it with a
  blank line, wrap at about 72 characters, and explain why, not which files changed.
- Keep one logical change per commit. Include related tests and documentation in the same commit.
- Mark breaking changes with `!` after the type or scope, and add a `BREAKING CHANGE:` footer.
- Reference the local spec or issue in a footer when one exists, for example
  `Refs: .scratch/object-selection.md`.

Examples from this repository:

```text
feat: add object selector flags
fix: surface the sf error message instead of a stack trace
test: cover sf JSON response parsing
docs: record sfdb build and layout in AGENTS.md
```

A commit with a body:

```text
feat: add progress display and verbose logging mode

Default clone and schema runs render an in-place status line and progress
bars. --verbose switches to structured logs and disables the progress
display. Redirected stderr gets throttled plain lines.
```

Before committing:

```bash
gofmt -w cmd internal
go build ./...
go test ./...
go test -race ./...
go vet ./...
git diff --cached
```

Do not commit access tokens, usernames, org-specific secrets, SQLite files, `bin/`, `dist/`, or
generated artifacts. `docs/` and `.scratch/` are local-only in this checkout; put conclusions that
must travel with the code into `AGENTS.md`, `README.md`, or `GLOSSARY.md`.

### Pull requests

`origin` is `https://github.com/KamilGolis/sf-db.git`. Use this shape:

- Title: the same Conventional Commit style as the primary commit, for example
  `feat: add object selector flags`.
- Keep the PR focused. Split unrelated work into separate PRs.
- Describe what changed and why, then list verification commands and results.
- Include a short before/after example for CLI behavior or output changes.
- Link the spec or issue under `.scratch/` and call out anything that only exists in the
  gitignored `docs/` directory.
- Never include tokens, usernames, or other credentials in the description or screenshots.
- Keep the target branch linear. Rebase before merging; squash a fixup-heavy branch into one
  Conventional Commit.

Suggested PR body:

```markdown
## What

Add `--all-objects`, `--custom-objects`, and `--standard-objects` to `clone` and `schema`.

## Why

Selecting objects one by one is slow on a large org. Selectors resolve through Describe Global and
exclude derived system objects by default.

## Verification

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- DEV: `--all-objects` resolves 721 objects

## Notes

`docs/adr/0002-object-selection.md` is local-only because `docs/` is gitignored.
```
