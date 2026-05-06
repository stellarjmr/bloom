package bloom

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type TaskStatus string

const (
	StatusOK      TaskStatus = "ok"
	StatusSkipped TaskStatus = "skipped"
	StatusDryRun  TaskStatus = "dry-run"
)

type UpdateOptions struct {
	DryRun bool
	Config Config
}

type TaskResult struct {
	Name        string
	Status      TaskStatus
	Message     string
	Summary     []string
	InstallHint string
	Output      string
	Err         error
	Duration    time.Duration
}

type Task struct {
	Name            string
	Description     string
	RequiredCommand string
	InstallHint     string
	Enabled         bool
	Run             func(context.Context, Runner, UpdateOptions) TaskResult
}

func BuildTasks(cfg Config) ([]Task, error) {
	builtins := map[string]Task{
		"brew": {
			Name:            "brew",
			Description:     "Update outdated Homebrew formulae",
			RequiredCommand: "brew",
			Run:             runBrewFormulae,
		},
		"cask": {
			Name:            "cask",
			Description:     "Update outdated Homebrew casks",
			RequiredCommand: "brew",
			Run:             runBrewCasks,
		},
		"amp": {
			Name:            "amp",
			Description:     "Run amp update",
			RequiredCommand: "amp",
			Run:             runAmp,
		},
		"yazi": {
			Name:            "yazi",
			Description:     "Update Yazi plugins",
			RequiredCommand: "ya",
			Run:             runYazi,
		},
		"nvim": {
			Name:            "nvim",
			Description:     "Update lazy.nvim and vim.pack Neovim plugins",
			RequiredCommand: "nvim",
			Run:             runNeovim,
		},
		"mason": {
			Name:            "mason",
			Description:     "Update Mason packages",
			RequiredCommand: "nvim",
			Run:             runMason,
		},
		"npm": {
			Name:            "npm",
			Description:     "Update global npm packages",
			RequiredCommand: "npm",
			Run:             runNPM,
		},
		"cleanup": {
			Name:            "cleanup",
			Description:     "Run brew cleanup",
			RequiredCommand: "brew",
			Run:             runBrewCleanup,
		},
	}

	tasks := make([]Task, 0, len(cfg.TaskOrder))
	for _, name := range cfg.TaskOrder {
		task, ok := builtins[name]
		if !ok {
			return nil, fmt.Errorf("unknown configured task %q", name)
		}
		taskCfg := cfg.Tasks[name]
		task.Enabled = taskCfg.Enabled
		task.InstallHint = taskCfg.InstallHint
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func requireCommand(r Runner, taskName, command, hint string) (TaskResult, bool) {
	if _, err := r.LookPath(command); err != nil {
		return TaskResult{
			Status:      StatusSkipped,
			Message:     "missing " + command,
			InstallHint: hint,
		}, false
	}
	return TaskResult{}, true
}

func runBrewFormulae(ctx context.Context, r Runner, opts UpdateOptions) TaskResult {
	if res, ok := requireCommand(r, "brew", "brew", opts.Config.Tasks["brew"].InstallHint); !ok {
		return res
	}
	outdated := r.Run(ctx, "brew", "outdated", "--quiet", "--formula")
	if outdated.Err != nil {
		return failed(outdated)
	}
	formulae := nonEmptyLines(outdated.Stdout)
	if len(formulae) == 0 {
		return TaskResult{Status: StatusOK, Message: "current"}
	}
	if opts.DryRun {
		return TaskResult{Status: StatusDryRun, Message: fmt.Sprintf("%d formulae", len(formulae)), Summary: prefixLines("would update brew formula ", formulae)}
	}
	update := r.Run(ctx, "brew", "update")
	if update.Err != nil {
		return failed(update)
	}
	args := append([]string{"upgrade"}, formulae...)
	upgrade := r.Run(ctx, "brew", args...)
	if upgrade.Err != nil {
		return failed(upgrade)
	}
	return TaskResult{Status: StatusOK, Message: fmt.Sprintf("%d updated", len(formulae)), Summary: prefixLines("updated brew formula ", formulae)}
}

func runBrewCasks(ctx context.Context, r Runner, opts UpdateOptions) TaskResult {
	if res, ok := requireCommand(r, "cask", "brew", opts.Config.Tasks["cask"].InstallHint); !ok {
		return res
	}
	outdated := r.Run(ctx, "brew", "outdated", "--quiet", "--cask", "--greedy")
	if outdated.Err != nil {
		return failed(outdated)
	}
	casks := nonEmptyLines(outdated.Stdout)
	if len(casks) == 0 {
		return TaskResult{Status: StatusOK, Message: "current"}
	}
	if opts.DryRun {
		return TaskResult{Status: StatusDryRun, Message: fmt.Sprintf("%d casks", len(casks)), Summary: prefixLines("would update brew cask ", casks)}
	}
	args := append([]string{"upgrade", "--cask", "--greedy"}, casks...)
	upgrade := r.Run(ctx, "brew", args...)
	if upgrade.Err != nil {
		return failed(upgrade)
	}
	return TaskResult{Status: StatusOK, Message: fmt.Sprintf("%d updated", len(casks)), Summary: prefixLines("updated brew cask ", casks)}
}

func runAmp(ctx context.Context, r Runner, opts UpdateOptions) TaskResult {
	if res, ok := requireCommand(r, "amp", "amp", opts.Config.Tasks["amp"].InstallHint); !ok {
		return res
	}
	if opts.DryRun {
		return TaskResult{Status: StatusDryRun, Message: "amp update"}
	}
	before := strings.TrimSpace(r.Run(ctx, "amp", "--version").Stdout)
	update := r.Run(ctx, "amp", "update")
	if update.Err != nil {
		return failed(update)
	}
	after := strings.TrimSpace(r.Run(ctx, "amp", "--version").Stdout)
	if before != "" && after != "" && before != after {
		return TaskResult{Status: StatusOK, Message: "updated", Summary: []string{"updated amp"}}
	}
	return TaskResult{Status: StatusOK, Message: "current"}
}

func runYazi(ctx context.Context, r Runner, opts UpdateOptions) TaskResult {
	if res, ok := requireCommand(r, "yazi", "ya", opts.Config.Tasks["yazi"].InstallHint); !ok {
		return res
	}
	beforeOut := r.Run(ctx, "ya", "pkg", "list")
	before := parseYaziPlugins(beforeOut.Stdout)
	if opts.DryRun {
		return TaskResult{Status: StatusDryRun, Message: fmt.Sprintf("%d plugins", len(before))}
	}
	upgrade := r.Run(ctx, "ya", "pkg", "upgrade")
	if upgrade.Err != nil {
		return failed(upgrade)
	}
	afterOut := r.Run(ctx, "ya", "pkg", "list")
	after := parseYaziPlugins(afterOut.Stdout)
	summary := diffVersionMap("updated yazi plugin ", "installed yazi plugin ", before, after)
	if len(summary) == 0 {
		return TaskResult{Status: StatusOK, Message: "current"}
	}
	return TaskResult{Status: StatusOK, Message: fmt.Sprintf("%d changed", len(summary)), Summary: summary}
}

func runNPM(ctx context.Context, r Runner, opts UpdateOptions) TaskResult {
	if res, ok := requireCommand(r, "npm", "npm", opts.Config.Tasks["npm"].InstallHint); !ok {
		return res
	}
	beforeOut := r.Run(ctx, "npm", "list", "-g", "--depth=0", "--json")
	if beforeOut.Err != nil {
		return failed(beforeOut)
	}
	before := parseNPMGlobals(beforeOut.Stdout)
	if opts.DryRun {
		return TaskResult{Status: StatusDryRun, Message: fmt.Sprintf("%d packages", len(before))}
	}
	update := r.Run(ctx, "npm", "update", "-g")
	if update.Err != nil {
		return failed(update)
	}
	afterOut := r.Run(ctx, "npm", "list", "-g", "--depth=0", "--json")
	after := parseNPMGlobals(afterOut.Stdout)
	summary := diffVersionMap("updated npm package ", "", before, after)
	if len(summary) == 0 {
		return TaskResult{Status: StatusOK, Message: "current"}
	}
	return TaskResult{Status: StatusOK, Message: fmt.Sprintf("%d updated", len(summary)), Summary: summary}
}

func runBrewCleanup(ctx context.Context, r Runner, opts UpdateOptions) TaskResult {
	if res, ok := requireCommand(r, "cleanup", "brew", opts.Config.Tasks["cleanup"].InstallHint); !ok {
		return res
	}
	if opts.DryRun {
		return TaskResult{Status: StatusDryRun, Message: "brew cleanup"}
	}
	cleanup := r.Run(ctx, "brew", "cleanup")
	if cleanup.Err != nil {
		return failed(cleanup)
	}
	return TaskResult{Status: StatusOK, Message: "done"}
}

func runNeovim(ctx context.Context, r Runner, opts UpdateOptions) TaskResult {
	if res, ok := requireCommand(r, "nvim", "nvim", opts.Config.Tasks["nvim"].InstallHint); !ok {
		return res
	}
	nvimDir := filepath.Join(configHome(), "nvim")
	lazyLock := filepath.Join(nvimDir, "lazy-lock.json")
	packLock := filepath.Join(nvimDir, "nvim-pack-lock.json")
	hasLazy := fileExists(lazyLock)
	hasPack := fileExists(packLock)
	if !hasLazy && !hasPack {
		return TaskResult{Status: StatusSkipped, Message: "no lockfiles"}
	}

	beforeLazy := map[string]string{}
	beforePack := map[string]string{}
	if hasLazy {
		beforeLazy = parseLazyLockFile(lazyLock)
	}
	if hasPack {
		beforePack = parsePackLockFile(packLock)
	}

	if opts.DryRun {
		parts := make([]string, 0, 2)
		if hasLazy {
			parts = append(parts, "lazy.nvim")
		}
		if hasPack {
			parts = append(parts, "vim.pack")
		}
		return TaskResult{Status: StatusDryRun, Message: strings.Join(parts, ", ")}
	}

	var combinedOutput strings.Builder
	if hasLazy {
		out := r.Run(ctx, "nvim", "--headless", "-i", "NONE", "+Lazy! sync", "+qa")
		combinedOutput.WriteString(out.Combined())
		if out.Err != nil {
			return TaskResult{Err: out.Err, Output: out.Combined()}
		}
	}
	if hasPack {
		// vim.pack.update({names}, {force=true}) applies updates without the interactive confirmation buffer.
		out := r.Run(ctx, "nvim", "--headless", "-i", "NONE", "+lua if vim.pack then vim.pack.update(nil, { force = true }) else error('vim.pack is not available') end", "+qa")
		combinedOutput.WriteString(out.Combined())
		if out.Err != nil {
			return TaskResult{Err: out.Err, Output: out.Combined()}
		}
	}

	afterLazy := map[string]string{}
	afterPack := map[string]string{}
	if hasLazy {
		afterLazy = parseLazyLockFile(lazyLock)
	}
	if hasPack {
		afterPack = parsePackLockFile(packLock)
	}

	summary := diffVersionMap("updated nvim plugin ", "installed nvim plugin ", beforeLazy, afterLazy)
	summary = append(summary, diffVersionMap("updated vim.pack plugin ", "installed vim.pack plugin ", beforePack, afterPack)...)
	if len(summary) == 0 {
		return TaskResult{Status: StatusOK, Message: "current", Output: combinedOutput.String()}
	}
	return TaskResult{Status: StatusOK, Message: fmt.Sprintf("%d changed", len(summary)), Summary: summary, Output: combinedOutput.String()}
}

func runMason(ctx context.Context, r Runner, opts UpdateOptions) TaskResult {
	if res, ok := requireCommand(r, "mason", "nvim", opts.Config.Tasks["mason"].InstallHint); !ok {
		return res
	}
	if opts.DryRun {
		return TaskResult{Status: StatusDryRun, Message: "mason registry"}
	}
	out := r.Run(ctx, "nvim", "--headless", "-i", "NONE", "+lua "+masonLua(), "+qa")
	if out.Err != nil {
		return failed(out)
	}
	var summary []string
	for _, line := range nonEmptyLines(out.Stdout) {
		if strings.HasPrefix(line, "MASON_UPDATED:") {
			summary = append(summary, "updated mason package "+strings.TrimPrefix(line, "MASON_UPDATED:"))
		}
	}
	if len(summary) == 0 {
		return TaskResult{Status: StatusOK, Message: "current"}
	}
	return TaskResult{Status: StatusOK, Message: fmt.Sprintf("%d updated", len(summary)), Summary: summary}
}

func failed(out CommandOutput) TaskResult {
	return TaskResult{Err: out.Err, Output: out.Combined()}
}

func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func prefixLines(prefix string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, prefix+value)
	}
	return out
}

func diffVersionMap(updatePrefix, installPrefix string, before, after map[string]string) []string {
	var names []string
	for name := range before {
		names = append(names, name)
	}
	sort.Strings(names)

	var summary []string
	for _, name := range names {
		oldVersion := before[name]
		newVersion := after[name]
		if newVersion != "" && newVersion != oldVersion {
			summary = append(summary, updatePrefix+name)
		}
	}
	if installPrefix == "" {
		return summary
	}

	names = names[:0]
	for name := range after {
		if before[name] == "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		summary = append(summary, installPrefix+name)
	}
	return summary
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func parseYaziPlugins(output string) map[string]string {
	plugins := map[string]string{}
	inPlugins := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch line {
		case "Plugins:":
			inPlugins = true
			continue
		case "Flavors:":
			inPlugins = false
			continue
		}
		if !inPlugins || line == "" {
			continue
		}
		name := line
		rev := ""
		if open := strings.LastIndex(line, "("); open >= 0 && strings.HasSuffix(line, ")") {
			name = strings.TrimSpace(line[:open])
			rev = strings.TrimSuffix(line[open+1:], ")")
		}
		if name != "" {
			plugins[name] = rev
		}
	}
	return plugins
}

func parseNPMGlobals(output string) map[string]string {
	type npmDep struct {
		Version string `json:"version"`
	}
	var parsed struct {
		Dependencies map[string]npmDep `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return map[string]string{}
	}
	result := map[string]string{}
	for name, dep := range parsed.Dependencies {
		if dep.Version != "" {
			result[name] = dep.Version
		}
	}
	return result
}

func parseLazyLockFile(path string) map[string]string {
	content, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	var parsed map[string]struct {
		Commit string `json:"commit"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		return map[string]string{}
	}
	result := map[string]string{}
	for name, item := range parsed {
		if item.Commit != "" {
			result[name] = item.Commit
		}
	}
	return result
}

func parsePackLockFile(path string) map[string]string {
	content, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	var parsed struct {
		Plugins map[string]struct {
			Rev string `json:"rev"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(content, &parsed); err != nil {
		return map[string]string{}
	}
	result := map[string]string{}
	for name, item := range parsed.Plugins {
		if item.Rev != "" {
			result[name] = item.Rev
		}
	}
	return result
}

func masonLua() string {
	return `
local pip_args = {}
local proxy = os.getenv('PIP_PROXY')
if proxy then
  pip_args = { '--proxy', proxy }
end

local ok_lazy, lazy = pcall(require, 'lazy')
if ok_lazy then
  lazy.load({ plugins = { 'mason.nvim' } })
end

local ok_mason, mason = pcall(require, 'mason')
if not ok_mason then
  error(('mason.nvim is not available: %s'):format(mason))
end

mason.setup({
  pip = {
    upgrade_pip = false,
    install_args = pip_args,
  },
  ui = {
    border = 'single',
    backdrop = 40,
    width = 0.7,
    height = 0.7,
    icons = {
      package_installed = '✓',
      package_pending = '➜',
      package_uninstalled = '✗',
    },
  },
})

local a = require('mason-core.async')
local registry = require('mason-registry')

a.run_blocking(function()
  local ok, result = a.wait(registry.update)
  a.scheduler()
  if not ok then
    error(('Failed to update Mason registries: %s'):format(vim.inspect(result)))
  end

  local outdated = {}
  for _, pkg in ipairs(registry.get_installed_packages()) do
    local current_version = pkg:get_installed_version()
    local latest_version = pkg:get_latest_version()
    if current_version ~= latest_version then
      table.insert(outdated, pkg)
    end
  end

  if #outdated == 0 then
    vim.api.nvim_out_write('MASON_NO_UPDATES\n')
    return
  end

  for _, pkg in ipairs(outdated) do
    a.wait(function(resolve, reject)
      pkg:install({}, function(success, install_result)
        (success and resolve or reject)(install_result)
      end)
    end)
    a.scheduler()
    vim.api.nvim_out_write(('MASON_UPDATED:%s\n'):format(pkg.name))
  end
end)
`
}
