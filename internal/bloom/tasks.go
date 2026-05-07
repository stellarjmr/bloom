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
	StatusRunning TaskStatus = "running"
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
			Status: StatusSkipped,
		}, false
	}
	return TaskResult{}, true
}

func runBrewFormulae(ctx context.Context, r Runner, opts UpdateOptions) TaskResult {
	if res, ok := requireCommand(r, "brew", "brew", opts.Config.Tasks["brew"].InstallHint); !ok {
		return res
	}
	if !opts.DryRun {
		update := r.Run(ctx, "brew", "update")
		if update.Err != nil {
			return failed(update)
		}
	}
	outdated := r.Run(ctx, "brew", "outdated", "--quiet", "--formula")
	if outdated.Err != nil {
		return failed(outdated)
	}
	formulae := filterNames(resolveBrewFormulaNames(ctx, r, nonEmptyLines(outdated.Stdout)), opts.Config.Tasks["brew"])
	if len(formulae) == 0 {
		return TaskResult{Status: StatusOK}
	}
	if opts.DryRun {
		return TaskResult{Status: StatusDryRun, Message: fmt.Sprintf("%d formulae", len(formulae)), Summary: summaryLines(formulae)}
	}
	args := append([]string{"upgrade"}, formulae...)
	upgrade := r.Run(ctx, "brew", args...)
	if upgrade.Err != nil {
		return failed(upgrade)
	}
	return TaskResult{Status: StatusOK, Message: fmt.Sprintf("%d changed", len(formulae)), Summary: summaryLines(formulae)}
}

func runBrewCasks(ctx context.Context, r Runner, opts UpdateOptions) TaskResult {
	if res, ok := requireCommand(r, "cask", "brew", opts.Config.Tasks["cask"].InstallHint); !ok {
		return res
	}
	if !opts.DryRun {
		update := r.Run(ctx, "brew", "update")
		if update.Err != nil {
			return failed(update)
		}
	}
	outdated := r.Run(ctx, "brew", "outdated", "--quiet", "--cask", "--greedy")
	if outdated.Err != nil {
		return failed(outdated)
	}
	casks := filterNames(nonEmptyLines(outdated.Stdout), opts.Config.Tasks["cask"])
	if len(casks) == 0 {
		return TaskResult{Status: StatusOK}
	}
	if opts.DryRun {
		return TaskResult{Status: StatusDryRun, Message: fmt.Sprintf("%d casks", len(casks)), Summary: summaryLines(casks)}
	}
	args := append([]string{"upgrade", "--cask", "--greedy"}, casks...)
	upgrade := r.Run(ctx, "brew", args...)
	if upgrade.Err != nil {
		return failed(upgrade)
	}
	return TaskResult{Status: StatusOK, Message: fmt.Sprintf("%d changed", len(casks)), Summary: summaryLines(casks)}
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
		return TaskResult{Status: StatusOK, Message: "changed", Summary: []string{"amp"}}
	}
	return TaskResult{Status: StatusOK}
}

func runYazi(ctx context.Context, r Runner, opts UpdateOptions) TaskResult {
	if res, ok := requireCommand(r, "yazi", "ya", opts.Config.Tasks["yazi"].InstallHint); !ok {
		return res
	}
	beforeOut := r.Run(ctx, "ya", "pkg", "list")
	before := parseYaziPlugins(beforeOut.Stdout)
	selected := filterNames(mapNames(before), opts.Config.Tasks["yazi"])
	before = pickVersionMap(before, selected)
	if opts.DryRun {
		return TaskResult{Status: StatusDryRun, Message: fmt.Sprintf("%d plugins", len(selected)), Summary: summaryLines(selected)}
	}
	if len(selected) == 0 {
		return TaskResult{Status: StatusOK}
	}
	args := append([]string{"pkg", "upgrade"}, selected...)
	upgrade := r.Run(ctx, "ya", args...)
	if upgrade.Err != nil {
		return failed(upgrade)
	}
	afterOut := r.Run(ctx, "ya", "pkg", "list")
	after := pickVersionMap(parseYaziPlugins(afterOut.Stdout), selected)
	summary := diffVersionMap(before, after)
	if len(summary) == 0 {
		return TaskResult{Status: StatusOK}
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
	selected := filterNames(mapNames(before), opts.Config.Tasks["npm"])
	before = pickVersionMap(before, selected)
	if opts.DryRun {
		return TaskResult{Status: StatusDryRun, Message: fmt.Sprintf("%d packages", len(selected)), Summary: summaryLines(selected)}
	}
	if len(selected) == 0 {
		return TaskResult{Status: StatusOK}
	}
	args := []string{"update", "-g"}
	if hasPackageFilter(opts.Config.Tasks["npm"]) {
		args = append(args, selected...)
	}
	update := r.Run(ctx, "npm", args...)
	if update.Err != nil {
		return failed(update)
	}
	afterOut := r.Run(ctx, "npm", "list", "-g", "--depth=0", "--json")
	after := pickVersionMap(parseNPMGlobals(afterOut.Stdout), selected)
	summary := diffVersionMap(before, after)
	if len(summary) == 0 {
		return TaskResult{Status: StatusOK}
	}
	return TaskResult{Status: StatusOK, Message: fmt.Sprintf("%d changed", len(summary)), Summary: summary}
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
	return TaskResult{Status: StatusOK, Message: "done", Summary: []string{"cleanup"}}
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
		return TaskResult{Status: StatusSkipped}
	}

	taskCfg := opts.Config.Tasks["nvim"]
	beforeLazy := map[string]string{}
	beforePack := map[string]string{}
	if hasLazy {
		beforeLazy = parseLazyLockFile(lazyLock)
	}
	if hasPack {
		beforePack = parsePackLockFile(packLock)
	}
	lazyNames := filterNames(mapNames(beforeLazy), taskCfg)
	packNames := filterNames(mapNames(beforePack), taskCfg)

	if opts.DryRun {
		parts := make([]string, 0, 2)
		if hasLazy && shouldRunPackageSet(lazyNames, taskCfg) {
			parts = append(parts, "lazy.nvim")
		}
		if hasPack && shouldRunPackageSet(packNames, taskCfg) {
			parts = append(parts, "vim.pack")
		}
		if len(parts) == 0 {
			return TaskResult{Status: StatusOK}
		}
		return TaskResult{Status: StatusDryRun, Message: strings.Join(parts, ", ")}
	}

	var combinedOutput strings.Builder
	if hasLazy && shouldRunPackageSet(lazyNames, taskCfg) {
		out := r.Run(ctx, "nvim", "--headless", "-i", "NONE", "+lua "+lazyLua(lazyNames, hasPackageFilter(taskCfg)), "+qa")
		combinedOutput.WriteString(out.Combined())
		if out.Err != nil {
			return TaskResult{Err: out.Err, Output: out.Combined()}
		}
	}
	if hasPack && shouldRunPackageSet(packNames, taskCfg) {
		// vim.pack.update({names}, {force=true}) applies updates without the interactive confirmation buffer.
		out := r.Run(ctx, "nvim", "--headless", "-i", "NONE", "+lua "+vimPackLua(packNames, hasPackageFilter(taskCfg)), "+qa")
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
	if hasPackageFilter(taskCfg) {
		beforeLazy = pickVersionMap(beforeLazy, lazyNames)
		afterLazy = pickVersionMap(afterLazy, lazyNames)
		beforePack = pickVersionMap(beforePack, packNames)
		afterPack = pickVersionMap(afterPack, packNames)
	}

	summary := diffVersionMap(beforeLazy, afterLazy)
	summary = append(summary, diffVersionMap(beforePack, afterPack)...)
	if len(summary) == 0 {
		return TaskResult{Status: StatusOK, Output: combinedOutput.String()}
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
	out := r.Run(ctx, "nvim", "--headless", "-i", "NONE", "+lua "+masonLua(opts.Config.Tasks["mason"].Include, opts.Config.Tasks["mason"].Exclude), "+qa")
	if out.Err != nil {
		return failed(out)
	}
	var summary []string
	for _, line := range nonEmptyLines(out.Combined()) {
		if strings.HasPrefix(line, "MASON_UPDATED:") {
			summary = append(summary, strings.TrimPrefix(line, "MASON_UPDATED:"))
		}
	}
	if len(summary) == 0 {
		return TaskResult{Status: StatusOK}
	}
	return TaskResult{Status: StatusOK, Message: fmt.Sprintf("%d changed", len(summary)), Summary: summary}
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

func summaryLines(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func diffVersionMap(before, after map[string]string) []string {
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
			summary = append(summary, name)
		}
	}

	names = names[:0]
	for name := range after {
		if before[name] == "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		summary = append(summary, name)
	}
	return summary
}

func filterNames(values []string, cfg TaskConfig) []string {
	available := map[string]bool{}
	for _, value := range values {
		available[value] = true
	}

	var filtered []string
	if len(cfg.Include) > 0 {
		seen := map[string]bool{}
		for _, value := range cfg.Include {
			if available[value] && !seen[value] {
				filtered = append(filtered, value)
				seen[value] = true
			}
		}
	} else {
		filtered = append(filtered, values...)
	}

	if len(cfg.Exclude) == 0 {
		return filtered
	}
	excluded := map[string]bool{}
	for _, value := range cfg.Exclude {
		excluded[value] = true
	}
	out := filtered[:0]
	for _, value := range filtered {
		if !excluded[value] {
			out = append(out, value)
		}
	}
	return out
}

func resolveBrewFormulaNames(ctx context.Context, r Runner, outdated []string) []string {
	if len(outdated) == 0 {
		return nil
	}
	out := r.Run(ctx, "brew", "list", "--formula", "--full-name")
	if out.Err != nil {
		return outdated
	}
	fullNamesByShort := map[string]string{}
	for _, name := range nonEmptyLines(out.Stdout) {
		fullNamesByShort[brewShortName(name)] = name
	}
	resolved := make([]string, 0, len(outdated))
	for _, name := range outdated {
		if fullName := fullNamesByShort[brewShortName(name)]; fullName != "" {
			resolved = append(resolved, fullName)
		} else {
			resolved = append(resolved, name)
		}
	}
	return resolved
}

func brewShortName(name string) string {
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

func hasPackageFilter(cfg TaskConfig) bool {
	return len(cfg.Include) > 0 || len(cfg.Exclude) > 0
}

func shouldRunPackageSet(names []string, cfg TaskConfig) bool {
	return !hasPackageFilter(cfg) || len(names) > 0
}

func mapNames(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func pickVersionMap(values map[string]string, names []string) map[string]string {
	out := make(map[string]string, len(names))
	for _, name := range names {
		if version, ok := values[name]; ok {
			out[name] = version
		}
	}
	return out
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

func lazyLua(names []string, filtered bool) string {
	plugins := "nil"
	if filtered {
		plugins = luaStringArray(names)
	}
	return strings.ReplaceAll(`
local ok_lazy, lazy = pcall(require, 'lazy')
if not ok_lazy then
  vim.api.nvim_out_write('LAZY_MISSING\n')
  return
end

local opts = { wait = true, show = false }
if __BLOOM_PLUGINS__ ~= nil then
  opts.plugins = __BLOOM_PLUGINS__
end
lazy.sync(opts)
`, "__BLOOM_PLUGINS__", plugins)
}

func vimPackLua(names []string, filtered bool) string {
	plugins := "nil"
	if filtered {
		plugins = luaStringArray(names)
	}
	return strings.ReplaceAll(`
if not vim.pack then
  vim.api.nvim_out_write('VIM_PACK_MISSING\n')
  return
end

vim.pack.update(__BLOOM_PLUGINS__, { force = true })
`, "__BLOOM_PLUGINS__", plugins)
}

func luaStringArray(values []string) string {
	if len(values) == 0 {
		return "{}"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ReplaceAll(value, `\`, `\\`)
		value = strings.ReplaceAll(value, `"`, `\"`)
		quoted = append(quoted, `"`+value+`"`)
	}
	return "{ " + strings.Join(quoted, ", ") + " }"
}

func masonLua(include, exclude []string) string {
	lua := `
local pip_args = {}
local proxy = os.getenv('PIP_PROXY')
if proxy then
  pip_args = { '--proxy', proxy }
end

local include = __BLOOM_INCLUDE__
local exclude = __BLOOM_EXCLUDE__
local include_set = {}
local exclude_set = {}
for _, name in ipairs(include) do
  include_set[name] = true
end
for _, name in ipairs(exclude) do
  exclude_set[name] = true
end

local function wants_package(name)
  if next(include_set) ~= nil and not include_set[name] then
    return false
  end
  return not exclude_set[name]
end

local ok_lazy, lazy = pcall(require, 'lazy')
if ok_lazy then
  lazy.load({ plugins = { 'mason.nvim' } })
end

local ok_mason, mason = pcall(require, 'mason')
if not ok_mason then
  vim.api.nvim_out_write('MASON_MISSING\n')
  return
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
      package_installed = '',
      package_pending = '⠋',
      package_uninstalled = '',
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
    if current_version ~= latest_version and wants_package(pkg.name) then
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
	lua = strings.ReplaceAll(lua, "__BLOOM_INCLUDE__", luaStringArray(include))
	lua = strings.ReplaceAll(lua, "__BLOOM_EXCLUDE__", luaStringArray(exclude))
	return lua
}
