# Changelog

Bloom records user-visible fixes, behavior changes, and features here so release notes stay traceable.

## Changelog rules

- Add user-visible changes to `Unreleased` before or with the commit that implements them.
- Keep entries concise and grouped as `Added`, `Changed`, or `Fixed`.
- When releasing, move `Unreleased` entries into `vX.Y.Z - YYYY-MM-DD` and leave a fresh `Unreleased` section.

## Unreleased

## v0.6.18 - 2026-05-25

### Added

- Added `bm history` to review recent clean and uninstall activity from Bloom's local operation logs.

### Changed

- Restored Homebrew cask `--zap` during `bm uninstall` so brew-installed apps are removed more completely.
- Deduplicate uninstall scan results by bundle ID so backup or mirrored app clones do not appear alongside the live app.
- Show the exact Homebrew cask `--zap` command in uninstall dry-runs and summaries when a cask is detected.
- Group `bm history` uninstall output by operation for easier review.

### Fixed

- Corrected `bm uninstall --list` help text to say it lists installed apps.

## v0.6.17 - 2026-05-23

### Added

- Added `CHANGELOG.md` and `CONTRIBUTING.md` so development history, release notes, and commit discipline are traceable.

### Fixed

- Avoid macOS authorization prompts from Background Task Management by removing the uninstall-time `sfltool` query (`fe73043`).
- Preserve `~/.config` during app uninstall by avoiding Homebrew cask `--zap` by default and filtering Bloom's own uninstall candidates (`344d5f5`).

## v0.6.16 - 2026-05-23

### Fixed

- Protected `~/.config` from `bm clean` targets.
- Kept Trash moves non-interactive by removing external Trash helper fallbacks.
