# Contributing

## Pull Request Titles

This repository uses semantic PR titles instead of enforcing semantic commit messages.

Use the format:

```text
type: summary
```

Optional scopes are also fine:

```text
type(scope): summary
```

Recommended types:

- `feat`
- `fix`
- `chore`
- `docs`
- `refactor`
- `test`
- `ci`
- `build`
- `perf`
- `revert`

Examples:

- `feat: add persistent queue deployment defaults`
- `fix(exporter): classify 401 as permanent`
- `docs: explain ScopeDB table initialization`

## Commits

Commits do not need to follow a semantic format.
Keep them focused and easy to review.
