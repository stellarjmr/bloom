# Changelog

Bloom records user-visible fixes, behavior changes, and features here so release notes stay traceable.

## Changelog rules

- Add user-visible changes to `Unreleased` before or with the commit that implements them.
- Keep entries concise and grouped as `Added`, `Changed`, or `Fixed`.
- When releasing, move `Unreleased` entries into `vX.Y.Z - YYYY-MM-DD` and leave a fresh `Unreleased` section.

## Unreleased

### Fixed

- Avoid macOS authorization prompts from Background Task Management by removing the uninstall-time `sfltool` query (`fe73043`).
- Preserve `~/.config` during app uninstall by avoiding Homebrew cask `--zap` by default and filtering Bloom's own uninstall candidates (`344d5f5`).

## v0.6.16 - 2026-05-23

### Fixed

- Protected `~/.config` from `bm clean` targets.
- Kept Trash moves non-interactive by removing external Trash helper fallbacks.
