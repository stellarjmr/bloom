# Changelog

Bloom records user-visible fixes, behavior changes, and features here so release notes stay traceable.

## Changelog rules

- Add user-visible changes to `Unreleased` before or with the commit that implements them.
- Keep entries concise and grouped as `Added`, `Changed`, or `Fixed`.
- When releasing, move `Unreleased` entries into `vX.Y.Z - YYYY-MM-DD` and leave a fresh `Unreleased` section.

## Unreleased

### Added

- Update the Antigravity CLI with `agy update` as the new `agy` task. An `agy` linked from the `antigravity-cli` Homebrew cask is skipped because the `cask` task already upgrades it. Configs saved by earlier versions pick the task up.

### Changed

- A config whose task `order` was edited by hand, for example to leave `amp` out, now picks up newly added tasks such as `claude` and `agy` too. Such an `order` used to stay as written, so `bm update` never ran `claude update` there. A `[tasks.<name>]` section with `enabled = false` keeps a new task off. v0.6.28 wrote that section for `claude` whenever it saved such a config; select `claude` under `bm config` → Tasks to turn it on.
- Uninstall the `steam` cask without `--zap` instead of refusing it. v0.6.28 refused that cask because its zap trashes the Steam library, and pointed to `brew uninstall --cask steam` without `--zap`; Bloom now runs that itself, keeps the library, and moves Steam's other leftovers to Trash.

### Fixed

- Keep what tools install under `~/Library/Caches`. `bm clean` swept `camoufox` and `Cypress`, where Camoufox and Cypress keep the browser and test runner they launch, so every run failed until a download of a gigabyte or more, and `Buzz`, which holds Buzz's downloaded Whisper models and installed plugins. All three are now protected, whatever the whitelist says.
- Stop Homebrew's cask uninstall from taking your own files. Zap lists reach past an app's Library data: every Ableton Live cask trashes `~/Music/Ableton`, Actual's trashes `~/Documents/Actual` with its budget files, OmniGraffle's trashes its iCloud Drive folder, Dropbox's trashes `~/Library/CloudStorage/Dropbox`, and over a hundred casks trash their folder in `~/.config`. `bm uninstall` now reads the uninstall and zap steps Homebrew will run. When only the zap would trash or delete something that exists in Desktop, Documents, Downloads, Movies, Music, Pictures, Public, iCloud Drive, a cloud storage folder, `~/.config`, or data Bloom keeps, Bloom runs `brew uninstall --cask` without `--zap`, moves its own leftover list to Trash, and the summary names what the zap would have taken. A few casks' plain uninstall steps reach such data too, such as Final Fantasy XIV's folder under `~/Documents/My Games`; those run with or without `--zap`, so Bloom refuses the cask, as it does when it cannot read the steps.
- Uninstall TeamViewer QuickSupport, QuickJoin, and TeamViewer Meeting again. v0.6.28 sent every app with a `com.teamviewer.` Bundle ID to TeamViewer's own uninstaller, but these are plain apps without services; only TeamViewer and TeamViewer Host still need it.
- Look for Apple Configurator firmware only in Configurator's own group containers. To find the `.ipsw` files that rule covers, `bm clean` walked every app's folder under `~/Library/Group Containers` on each run, which took longer the more apps were installed and read other apps' data on the way.

## v0.6.28 - 2026-11-03

### Added

- Update Claude Code's native install with `claude update` as the new `claude` task. A Homebrew cask or npm copy is skipped because the `cask` and `npm` tasks already update it, and configs saved by earlier versions pick the task up unless their `order` was curated by hand.

### Fixed

- Never clean llama.cpp's downloaded models: `bm clean` swept `~/Library/Caches/llama.cpp`, where `llama-cli -hf` and `llama-server -hf` keep multi-GB GGUF weights, as an ordinary app cache. The folder, and a relocated `LLAMA_CACHE`, now count as model stores.
- Keep the Steam library when `bm uninstall` removes Steam: the name match took `~/Library/Application Support/Steam`, which holds installed games (`steamapps`) and saves (`userdata`). The folder now stays and the summary names it. The `steam` cask's zap removes the same folder, so Bloom refuses that cask and points to `brew uninstall --cask steam` without `--zap`.
- Stop reporting a failed Homebrew cask uninstall as a success. When `brew uninstall --cask` could not run, for example before the Xcode license is accepted, Bloom printed `ran:`, moved the app itself, and left Homebrew's receipt behind, so a later `brew upgrade` could reinstall the app. Bloom now checks that Homebrew no longer lists the cask, and otherwise stops for that app with Homebrew's own error.
- Keep the data that another installed build of the same app uses. Uninstalling Firefox while Firefox Nightly was installed moved the shared `~/Library/Application Support/Firefox` profiles, and uninstalling Google Chrome took Chrome Canary's caches, saved state, and preferences. Bloom now recognizes builds whose names differ only by a release channel or whose Bundle IDs extend each other, keeps what they share, and refuses a Homebrew cask while another build is installed, because channel casks zap each other's data.
- Refuse to move TeamViewer, Adobe Genuine Service, Alibaba Cloud Security, and the Avast, AVG, Malwarebytes, Bitdefender, Intego, Kaspersky, Norton, and Trend Micro security apps to Trash, and point to the vendor's own uninstaller instead: moving only the app left their daemons, helpers, or system extensions running. The Bitdefender, Intego, Norton, and Trend Micro bundle IDs are not yet confirmed against the shipping apps.
- Hold uv's and Bun's caches while they are in use. `bm clean` moved entries of `~/.cache/uv` out from under tools and MCP servers that `uvx` runs from that cache, and Bun's install cache during a `bun install`. Both now wait until no `uv`, `uvx`, `bun`, or `bunx` process runs.
- List the live app instead of a hidden backup in `bm uninstall`. A `.Aegis-backup-1.app` copy that a manual reinstall left beside `Aegis.app` shares its Bundle ID and could win the duplicate check, so the menu showed the backup, uninstalling removed only that copy, and the real app stayed hidden. A dot-named copy now ranks below every visible one and is listed only when no visible copy exists.
- Match an app's executable name literally when `bm uninstall` checks whether the app runs. `pgrep` read the name as a regular expression, so a name with parentheses failed to compile and the app looked closed, which skipped quitting it before its bundle moved to Trash; a name such as `.*` matched every process.
- Hold VS Code's caches while VS Code runs. `bm clean` moved its `Cache`, `CachedData`, `GPUCache`, and `CachedExtensionVSIXs` folders under `~/Library/Application Support/Code` out from under the running editor, because its processes run as `Electron` and no owner check covered that folder. They now wait until no process from `Visual Studio Code.app` and no `Code Helper` process runs.
- Offer a whitelist row for Bun's install cache. `bm clean` cleans `~/.bun/install/cache`, but the whitelist menu had no row for it, so it could be kept only by editing the config by hand.
- Stop cleaning Jupyter's runtime folder. `bm clean` treated `~/.jupyter/runtime` as a cache and moved the connection files of running servers and kernels, which broke `jupyter server list`, `--existing`, and editor reconnects. The folder is now protected, and its whitelist row, which doubled as the clean rule, is gone.

## v0.6.27 - 2026-10-16

### Fixed

- Stop `bm uninstall` from claiming other apps' Group Containers and bundle-ID data: a leftover must carry the app's complete bundle ID on dot boundaries, so vendor-shared containers such as Microsoft's OneAuth, Entra, Defender, and Office shared containers, or Podcasts' container for TestFlight, are no longer removed together with one product.
- Match `bm uninstall` name-based leftovers literally: the bundle name and its no-space, underscore, and hyphen spellings count only in Application Support, Caches, Logs, and Saved Application State, so another product whose name extends the app's (for example `Telegram Desktop` for `Telegram`) or whose bundle ID shares the app's vendor component is no longer removed.
- Unload and remove only the LaunchAgents an uninstalled app owns: an agent must be named `<bundleID>.plist` or start an executable inside the selected bundle. Another product's agent that only watches or mentions the app, such as Riot Client's install checker for League of Legends, and a sibling channel's agent stay loaded, and a plist replaced or rewritten after planning is kept.
- Stop with a config error instead of loading defaults when the config path is a broken symlink or runs through a broken directory link, so `bm clean` never runs without the user's whitelist when a dotfiles link or its volume is unavailable.
- Escape control characters in clean and uninstall logs and in `bm history` output, so a crafted filename can no longer forge a successful cleanup record or send terminal control sequences. Rows written by older versions are escaped when displayed, and backslashes stay literal.
- Keep the `~/Library/Logs/DiagnosticReports` directory during `bm clean` and clean only the reports inside it; moving the directory to Trash stopped macOS from saving later crash reports.
- Never clean Google's `GIPPseudonymousID` identity store or `CCTClearcutLogger` queue under `~/Library/Caches`; removing them while Gemini runs made it rewrite data to disk in an unbounded loop.
- Keep the default Gradle build-cache protection when the clean whitelist menu is saved. The default and its menu row used different spellings, so the row showed unchecked and any save, even without changes, dropped the protection; older configs are now read with the row's spelling, and unticking the row takes effect.
- Offer whitelist rows for pip's and Yarn v1's macOS caches under `~/Library/Caches`; the existing rows named only `~/.cache` paths, so ticking them left the caches those tools actually use cleanable.
- Stop cleaning `~/.gitconfig.lock`, and protect `~/.gitconfig.bak*`: Git holds the lock while it writes the config and renames it into place, so moving it to Trash mid-write could lose that change, and a backup can be the only copy of earlier settings.
- Stop `bm uninstall` from claiming every Electron app's crash reports when the selected app's executable is still named `Electron`, and from treating another running Electron app as the selected one. Crash reports fall back to the app's name, and process checks count only processes inside the selected bundle.
- Keep an app's remaining data, Login Item, LaunchServices record, and Dock tile when an updater or a quick reinstall puts the app back after `bm uninstall` moved its bundle to Trash. The app path is checked again before each later step, including Trash retries and the Dock cleanup at the end of a batch, and a path that cannot be checked keeps them as well.
- Bound every best-effort uninstall teardown step with a time limit (30 seconds for the graceful quit, 5 seconds for LaunchAgent unloads, Login Item removal, helper bootout, and Dock edits), so an Automation prompt or an unresponsive app no longer stalls each app in a batch for the two-minute Apple Event timeout.

## v0.6.26 - 2026-09-07

### Added

- Reclaim verified older Autodesk Fusion webdeploy bundles while retaining current, equal, newer, malformed, redirected, active, open, or changed versions and moving every eligible version to Trash.

### Changed

- Show localized Finder-facing app names in uninstall lists and summaries without using those labels for leftover matching, process control, cask detection, or bundle identity.

### Fixed

- Treat stale SQLite `-shm` sidecars as cleanup candidates when bounded open-file checks prove the database family is idle, while continuing to fail closed on live or inconclusive state.
- Preserve durable developer state during cleaning, including model/data stores, Deno state, Poetry virtual environments, Cargo sources/git state, and Rust toolchain documentation; retain precise cleanup for Poetry downloads, Cargo archives, and Rustup downloads.
- Keep complete Maven, Go module, SBT/Ivy, Cabal, Conda, Ollama, and R renv stores off the automatic clean path instead of treating dependency, toolchain, model, or environment roots as disposable caches.
- Reject the exact shared developer roots `~/.local`, `~/.config`, and `~/.cache` during uninstall discovery and again at the final Trash boundary.
- Stop the remaining clean run when a process or SQLite/open-file safety probe times out or is interrupted, reporting the current target as failed instead of continuing with partial evidence.
- Require complete `lsof` visibility before an idle result can authorize cleanup, treat successful status as positive open-file evidence, and recheck incomplete downloads after sizing and at the Trash boundary.
- Disable ANSI styling automatically when output is redirected or `TERM=dumb`, while continuing to honor `NO_COLOR` in both the shell front controller and Go progress output.
- Avoid restarting unrelated app extensions and network tunnels after uninstall by replacing the domain-wide LaunchServices rebuild with bounded targeted unregister and garbage collection.
- Handle `.app` suffixes case-insensitively throughout uninstall name extraction, so bundles ending in `.APP` or mixed-case variants keep correct display, matching, and cask names.
- Revalidate an app process's start identity and bundle executable immediately before both `TERM` and `KILL`, preventing PID reuse from redirecting an uninstall signal to another process.

## v0.6.25 - 2026-08-21

### Fixed

- Force-refresh Homebrew metadata before checking or upgrading packages so newly published third-party tap updates are not missed by Homebrew's API fast path.

## v0.6.24 - 2026-08-15

### Fixed

- Update all Yazi plugins and flavors with `ya pkg upgrade` by default; only pass package IDs when Yazi include/exclude filters are configured.

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
