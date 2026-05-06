# bm

`bm` is a config-driven terminal updater for developer tools on macOS.

It is designed to replace one-off update scripts with a fast shortcut:

`bm` opens the interactive menu. `bm update` runs the updater directly.

## Install

The intended install path is Homebrew:

```bash
brew tap stellarjmr/tool
brew install stellarjmr/tool/bloom
```

The Homebrew formula is published from the `stellarjmr/tool` tap.
Homebrew installs prebuilt release binaries, so Go is only needed when building from source.

## Commands

```bash
bm                           # open the interactive menu
bm update                    # run enabled update tasks
bm update --dry-run          # inspect selected tasks without updating
bm update --only nvim        # run one task
bm update --skip npm         # skip a task
bm list                      # list configured tasks
bm doctor                    # show available and missing tools
bm config path               # print config path
bm config init               # create ~/.config/bloom/config.toml
```

## Default Tasks

The built-in task set mirrors the original `update-all.sh` workflow:

- `brew`: Homebrew formula updates
- `cask`: Homebrew cask updates
- `amp`: `amp update`
- `yazi`: Yazi plugin updates
- `nvim`: Neovim plugin updates for both lazy.nvim/LazyVim and `vim.pack`
- `mason`: Mason package updates
- `npm`: global npm package updates

Missing tools are skipped during `bm update` and are not counted in the progress total.

## Config

Default path:

```bash
~/.config/bloom/config.toml
```

Create it with:

```bash
bm config init
```

The config supports task order, enable/disable switches, per-task package `include`/`exclude` filters, progress width, and color output. Empty filters mean update everything that exists. See `config.example.toml`.

## Neovim

`bm` supports both common plugin paths:

- LazyVim/lazy.nvim: detects `lazy-lock.json` and runs headless `Lazy! sync`.
- Native `vim.pack`: detects `nvim-pack-lock.json` and runs headless `vim.pack.update(nil, { force = true })`.

If both lockfiles exist, lazy.nvim runs first and `vim.pack` runs second.
