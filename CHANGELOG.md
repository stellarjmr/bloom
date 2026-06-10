# Changelog

Bloom records user-visible fixes, behavior changes, and features here so release notes stay traceable.

## Changelog rules

- Add user-visible changes to `Unreleased` before or with the commit that implements them.
- Keep entries concise and grouped as `Added`, `Changed`, or `Fixed`.
- When releasing, move `Unreleased` entries into `vX.Y.Z - YYYY-MM-DD` and leave a fresh `Unreleased` section.

## Unreleased

## v0.6.20 - 2026-06-10

### Changed

- Build release `bm-core` binaries with CGO disabled so release artifacts stay pure-Go across macOS SDK changes.

### Fixed

- Stop `bm uninstall` leftover matching from claiming unrelated app data: very short app names no longer fuzzy-match shared `~/Library` entries, non-exact name matches respect Bloom's protected-data lists, and Visual Studio Code data dirs are reserved for the real VS Code apps.
- Require the Homebrew cask fallback detection to verify cask artifacts before running `brew uninstall --zap`, so a same-named cask can no longer permanently delete data for a manually installed app.
- Detect Neovim-driven task failures (lazy.nvim, vim.pack, Mason updates, checks, and removals) with completion sentinels, because headless nvim exits 0 even when its Lua chunk fails; Mason now also reports skipped instead of ok when mason.nvim is not installed.
- Apply the `NO_COLOR` environment override at render time only, so config write commands run under `NO_COLOR` no longer silently persist `color = false` into the config file.
- Keep interactive menus alive when buffered key input is drained under newer bash versions, instead of aborting and leaving the terminal stuck in the alternate screen.
- Restore the terminal alternate screen and cursor on every exit path of interactive menus, including unexpected script aborts and hangups.
- Avoid running duplicate `brew update` commands during one `bm update` and retry briefly when Homebrew's update lock is busy.
- Include per-app `~/Library/Application Support/CrashReporter/*.plist` leftovers in `bm uninstall` cleanup plans.
- Protect Raycast cache/state paths from `bm clean` because recent Raycast cache layouts may contain user data.

## v0.6.19 - 2026-05-26

### Changed

- Support administrator-authorized Trash moves for root-owned app bundles, including Mac App Store apps, without requiring external uninstall tools.

### Fixed

- Prevent `bm uninstall` from partially moving sandbox leftovers when the app bundle itself cannot be moved to Trash.

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
