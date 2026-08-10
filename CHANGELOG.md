# Changelog

Bloom records user-visible fixes, behavior changes, and features here so release notes stay traceable.

## Changelog rules

- Add user-visible changes to `Unreleased` before or with the commit that implements them.
- Keep entries concise and grouped as `Added`, `Changed`, or `Fixed`.
- When releasing, move `Unreleased` entries into `vX.Y.Z - YYYY-MM-DD` and leave a fresh `Unreleased` section.

## Unreleased

## v0.6.23 - 2026-08-10

### Changed

- Always merge hard clean-safety entries into existing whitelist configs, so older or replacement-style configs cannot silently enable Finder metadata cleanup.
- Keep active application and developer-tool caches out of both clean previews and real runs, including live SQLite families, and recheck process/open-file state immediately before moving anything to Trash.
- Report clean and uninstall totals using physical disk occupancy, bound directory size probes, and skip an unmeasurable clean target instead of trusting partial `du` output or stalling the remaining scan.
- Clean Cargo registry archives, extracted sources, git caches, and Rustup downloads from validated `CARGO_HOME`/`RUSTUP_HOME` locations while preserving registry indexes and installed binaries, refusing active builds, redirected cache roots, and targets replaced during probing.
- Reclaim abandoned Codex Desktop Sparkle and marketplace staging with exact path/identity gates, 30-day age fallback, version-aware pending-update protection, and process/open-file rechecks; completed marketplaces, backups, sessions, credentials, and configuration remain out of scope.
- Protect apps that share a Bundle ID during uninstall: re-scan before side effects, move only the selected non-cask bundle while preserving shared state, and refuse the required Homebrew `--zap` when another copy exists or the scan is incomplete.
- Find exact extended data names such as `AyuGramDesktop` and `AyuGram Desktop` from a Bundle ID leaf only when it demonstrably extends the app's own display name, avoiding wrapper/fork collisions and broad filename matching.
- Read iOS/iPadOS `Wrapper/*.app/Info.plist` identities during app discovery and sibling checks, while treating unreadable or malformed candidate metadata as an incomplete safety scan.
- Clean mise/XDG pnpm store generations under `~/.local/share/pnpm/store` with the same active pnpm/Corepack and redirected-path guards as the standard macOS store, keeping every Bloom removal recoverable in Trash.

### Fixed

- Pin the selected app bundle's filesystem identity throughout uninstall planning so a delete-and-recreate race cannot disguise a replacement bundle through inode reuse.

## v0.6.22 - 2026-07-29

### Fixed

- Prevent repeated `j`/`k` navigation keys from being echoed onto rows in the Check, Remove, and Uninstall package/app selector.

## v0.6.21 - 2026-06-27

### Changed

- Extend `bm uninstall` leftover scanning to embedded helper bundles: XPC services, app extensions (`.appex`), and login-item `.app` helpers inside an app now contribute their own bundle IDs, so per-helper `~/Library` data (Application Support, Caches, Preferences, LaunchAgents, and similar) is cleaned too. Matching uses exact embedded bundle IDs only and skips protected IDs (Apple system, password managers, security/network tooling), keeping removal scoped to the app's own data.

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
