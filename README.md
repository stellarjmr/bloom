# Bloom

<p align="center">
  <img src="assets/bloom_logo.png" alt="Bloom logo" width="150">
</p>

Bloom is a config-driven terminal updater for developer tools on macOS. The command is intentionally short: `bm` opens the menu, and `bm update` runs the updater directly.

![Bloom menu](assets/UI.png)

## Install

```bash
brew tap stellarjmr/tool
brew install stellarjmr/tool/bloom
```

The Homebrew formula installs prebuilt release binaries, so Go is only needed when building from source.

## Commands

```bash
bm                           # open the interactive menu
bm update                    # run enabled update tasks
bm update --dry-run          # inspect selected tasks without updating
bm update --only nvim        # run one task
bm update --skip npm         # skip a task
bm list                      # list configured tasks
bm doctor                    # show available and missing tools
bm config                    # open the interactive config menu
bm config path               # print config path
bm config init               # create ~/.config/bloom/config.toml
```

Bloom uses Nerd Font icons in the menu and progress states. Use a Nerd Font in your terminal for the intended display.

## Default Tasks

The default task set updates everything Bloom can detect:

- `brew`: Homebrew formula updates
- `cask`: Homebrew cask updates
- `amp`: `amp update`
- `yazi`: Yazi plugin updates
- `nvim`: Neovim plugin updates for lazy.nvim/LazyVim and `vim.pack`
- `mason`: Mason package updates
- `npm`: global npm package updates

Missing tools are skipped during `bm update` and are not counted in the progress total. For Homebrew updates, Bloom refreshes Homebrew metadata before checking outdated formulae and casks, so packages from tapped repositories are included.

## Config

Default path:

```bash
~/.config/bloom/config.toml
```

Create it with:

```bash
bm config init
```

The config controls task order, enable/disable switches, per-task package `include`/`exclude` filters, progress width, and color output. Empty filters mean update every detected package. Run `bm config` to manage tasks and package filters with a Space-select menu, or edit the TOML directly. See `config.example.toml`.

## Neovim

Bloom supports both common plugin paths:

- LazyVim/lazy.nvim: detects `lazy-lock.json` and runs headless `lazy.sync({ wait = true, show = false })`.
- Native `vim.pack`: detects `nvim-pack-lock.json` and runs headless `vim.pack.update(..., { force = true })`.

If both lockfiles exist, lazy.nvim runs first and `vim.pack` runs second.
