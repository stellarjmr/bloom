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

## Release smoke checklist

Do not start this checklist unless a release is being considered. Passing it does not publish anything; tagging, pushing, publishing, and Homebrew updates still require an explicit release request.

### Automated checks

```bash
go test ./...
bash -n bm
make build VERSION=dev
```

### Uninstall smoke

```bash
bm uninstall --list
bm uninstall --dry-run --app /Applications/<brew-cask-app>.app
```

Confirm that:

- Duplicate copies of the same app do not appear in the uninstall list.
- A detected Homebrew cask shows `would run: brew uninstall --cask --force --zap <token>` during dry-run.
- Apple system apps are absent from the menu/list.
- The uninstall path does not trigger macOS password, authorization, Finder, or AppleScript prompts.

### Clean smoke

```bash
bm clean --dry-run
```

Confirm that:

- `~/.config` is not targeted.
- Keychains, Mail, Photos, Messages, Safari/Cookies, browser history/cookies, LaunchAgents, LaunchDaemons, and other high-value user data are not targeted.
- Output remains readable without Nerd Font glyphs.

### Interactive menu smoke

```bash
bm
```

Confirm that:

- The menu enters and exits the terminal alternate screen cleanly.
- `○`/`●` multi-select and `Enter` behavior work in Check and Uninstall menus.
- Non-menu commands return to the normal terminal before printing output.

### Final release readiness

- `CHANGELOG.md` has complete `Unreleased` entries for all behavior changes being shipped.
- User-reported regressions have been retested by the user when practical.
- No debug, diagnostic, or speculative workaround output remains.
