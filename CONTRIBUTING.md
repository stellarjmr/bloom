# Contributing

## Development discipline

- One commit should solve one logical issue: one bug fix, one behavior change, one feature, or one focused docs/process update.
- Do not bundle unrelated cleanup, refactors, and behavior changes into the same commit.
- If a user-visible behavior changes, update `CHANGELOG.md` in the same work item.
- Keep fixes verifiable: record the focused validation that proves the change works.
- Do not release speculative fixes. Release only after local validation and, for user-reported regressions, user retesting when practical.

## Changelog workflow

- Add new entries under `## Unreleased` in `CHANGELOG.md`.
- Use `Added`, `Changed`, and `Fixed` headings when they apply.
- Before tagging a release, review `Unreleased`, move its entries to `## vX.Y.Z - YYYY-MM-DD`, and confirm no implemented behavior is missing from the release notes.
