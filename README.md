# bloom

`bloom` is a config-driven terminal updater for developer tools on macOS.

It is designed to replace one-off update scripts with a single command:

```bash
bloom update
```

## Install

The intended install path is Homebrew:

```bash
brew tap stellarjmr/tool
brew install stellarjmr/tool/bloom
```

The Homebrew formula is published from the `stellarjmr/tool` tap.

## Commands

```bash
bloom update                 # run enabled update tasks
bloom update --dry-run       # inspect selected tasks without updating
bloom update --only nvim     # run one task
bloom update --skip npm      # skip a task
bloom list                   # list configured tasks
bloom doctor                 # show missing tools and install hints
bloom config path            # print config path
bloom config init            # create ~/.config/bloom/config.toml
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
- `cleanup`: `brew cleanup`

Missing tools are not installed automatically. `bloom doctor` prints the preferred installation command, usually via Homebrew.

## Config

Default path:

```bash
~/.config/bloom/config.toml
```

Create it with:

```bash
bloom config init
```

The config supports task order, enable/disable switches, install hints, progress width, and color output. See `config.example.toml`.

## Neovim

`bloom` supports both common plugin paths:

- LazyVim/lazy.nvim: detects `lazy-lock.json` and runs headless `Lazy! sync`.
- Native `vim.pack`: detects `nvim-pack-lock.json` and runs headless `vim.pack.update(nil, { force = true })`.

If both lockfiles exist, lazy.nvim runs first and `vim.pack` runs second.
