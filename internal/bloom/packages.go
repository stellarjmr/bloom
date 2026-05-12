package bloom

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

type PackageRef struct {
	Task string
	Name string
}

type RemoveResult struct {
	Package PackageRef
	DryRun  bool
	Output  string
	Err     error
}

var removablePackageTasks = []string{"brew", "yazi", "mason", "npm"}

func ListTaskPackages(ctx context.Context, r Runner, task string) ([]string, error) {
	switch task {
	case "brew":
		if _, err := r.LookPath("brew"); err != nil {
			return nil, nil
		}
		out := r.Run(ctx, "brew", "list", "--formula", "--full-name")
		if out.Err != nil {
			return nil, out.Err
		}
		return nonEmptyLines(out.Stdout), nil
	case "cask":
		if _, err := r.LookPath("brew"); err != nil {
			return nil, nil
		}
		out := r.Run(ctx, "brew", "list", "--cask", "-1")
		if out.Err != nil {
			return nil, out.Err
		}
		return nonEmptyLines(out.Stdout), nil
	case "yazi":
		if _, err := r.LookPath("ya"); err != nil {
			return nil, nil
		}
		out := r.Run(ctx, "ya", "pkg", "list")
		if out.Err != nil {
			return nil, out.Err
		}
		return mapNames(parseYaziPlugins(out.Stdout)), nil
	case "nvim":
		return mapNames(loadNeovimPackages()), nil
	case "mason":
		if _, err := r.LookPath("nvim"); err != nil {
			return nil, nil
		}
		out := r.Run(ctx, "nvim", "--headless", "-i", "NONE", "+lua "+masonListLua(), "+qa")
		if out.Err != nil {
			return nil, out.Err
		}
		var names []string
		for _, line := range nonEmptyLines(out.Combined()) {
			if strings.HasPrefix(line, "MASON_PACKAGE:") {
				names = append(names, strings.TrimPrefix(line, "MASON_PACKAGE:"))
			}
		}
		return uniqueStrings(names), nil
	case "npm":
		if _, err := r.LookPath("npm"); err != nil {
			return nil, nil
		}
		out := r.Run(ctx, "npm", "list", "-g", "--depth=0", "--json")
		if out.Err != nil {
			return nil, out.Err
		}
		return mapNames(parseNPMGlobals(out.Stdout)), nil
	case "amp":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown task %q", task)
	}
}

func ListRemovablePackages(ctx context.Context, r Runner) ([]PackageRef, error) {
	items := []PackageRef{}
	for _, task := range removablePackageTasks {
		packages, err := ListTaskPackages(ctx, r, task)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", task, err)
		}
		for _, name := range packages {
			items = append(items, PackageRef{Task: task, Name: name})
		}
	}
	return items, nil
}

func loadNeovimPackages() map[string]string {
	nvimDir := filepath.Join(configHome(), "nvim")
	plugins := map[string]string{}
	for name, version := range parseLazyLockFile(filepath.Join(nvimDir, "lazy-lock.json")) {
		plugins[name] = version
	}
	for name, version := range parsePackLockFile(filepath.Join(nvimDir, "nvim-pack-lock.json")) {
		plugins[name] = version
	}
	return plugins
}

func masonListLua() string {
	return masonLocateLua() + `

local install_root = vim.fn.stdpath('data') .. '/mason'

local ok_settings, settings = pcall(require, 'mason.settings')
if ok_settings and settings and settings.current and settings.current.install_root_dir then
  install_root = settings.current.install_root_dir
else
  local ok_mason, mason = pcall(require, 'mason')
  if ok_mason then
    pcall(mason.setup, {})
    if settings and settings.current and settings.current.install_root_dir then
      install_root = settings.current.install_root_dir
    end
  end
end

local packages_dir = install_root .. '/packages'
if vim.fn.isdirectory(packages_dir) == 0 then
  return
end

local entries = vim.fn.readdir(packages_dir)
table.sort(entries)
for _, name in ipairs(entries) do
  if name ~= '.' and name ~= '..' then
    vim.api.nvim_out_write(('MASON_PACKAGE:%s\n'):format(name))
  end
end
`
}

func RemovePackages(ctx context.Context, r Runner, refs []PackageRef, dryRun bool) []RemoveResult {
	results := make([]RemoveResult, len(refs))
	type group struct {
		names   []string
		indexes []int
	}
	groups := map[string]*group{}
	order := []string{}

	for i, ref := range refs {
		results[i] = RemoveResult{Package: ref, DryRun: dryRun}
		if dryRun {
			continue
		}
		g := groups[ref.Task]
		if g == nil {
			g = &group{}
			groups[ref.Task] = g
			order = append(order, ref.Task)
		}
		g.names = append(g.names, ref.Name)
		g.indexes = append(g.indexes, i)
	}
	if dryRun {
		return results
	}

	for _, task := range order {
		g := groups[task]
		output, err := removePackageGroup(ctx, r, task, g.names)
		for _, idx := range g.indexes {
			results[idx].Output = output
			results[idx].Err = err
		}
	}
	return results
}

func removePackageGroup(ctx context.Context, r Runner, task string, names []string) (string, error) {
	if len(names) == 0 {
		return "", nil
	}
	switch task {
	case "brew":
		if err := requirePackageCommand(r, "brew"); err != nil {
			return "", err
		}
		args := append([]string{"uninstall"}, names...)
		return packageCommandResult(r.Run(ctx, "brew", args...))
	case "yazi":
		if err := requirePackageCommand(r, "ya"); err != nil {
			return "", err
		}
		args := append([]string{"pkg", "delete"}, names...)
		return packageCommandResult(r.Run(ctx, "ya", args...))
	case "mason":
		if err := requirePackageCommand(r, "nvim"); err != nil {
			return "", err
		}
		out := r.Run(ctx, "nvim", "--headless", "-i", "NONE", "+lua "+masonRemoveLua(names), "+qa")
		return packageCommandResult(out)
	case "npm":
		if err := requirePackageCommand(r, "npm"); err != nil {
			return "", err
		}
		args := append([]string{"uninstall", "-g"}, names...)
		return packageCommandResult(r.Run(ctx, "npm", args...))
	default:
		return "", fmt.Errorf("task %q does not support package removal", task)
	}
}

func requirePackageCommand(r Runner, command string) error {
	if _, err := r.LookPath(command); err != nil {
		return fmt.Errorf("%s not found", command)
	}
	return nil
}

func packageCommandResult(out CommandOutput) (string, error) {
	output := strings.TrimSpace(out.Combined())
	if out.Err != nil {
		return output, commandError(out)
	}
	return output, nil
}

func masonRemoveLua(names []string) string {
	lua := `
local names = __BLOOM_NAMES__

local ok_mason, mason = pcall(require, 'mason')
if not ok_mason then
  error('mason.nvim is unavailable')
end

mason.setup({})

local a = require('mason-core.async')
local registry = require('mason-registry')

local function reload_sources()
  if not registry.sources or not registry.sources.iterate then
    return
  end
  for source in registry.sources:iterate({ include_uninstalled = true }) do
    if source and type(source.reload) == 'function' then
      pcall(source.reload, source)
    end
  end
end

local function find_package(name)
  if registry.sources and registry.sources.iterate then
    for source in registry.sources:iterate({ include_uninstalled = true }) do
      if source and type(source.get_package) == 'function' then
        local ok_pkg, pkg = pcall(source.get_package, source, name)
        if ok_pkg and pkg then
          local id = pkg.spec and pkg.spec.source and pkg.spec.source.id or ''
          if not id:match('^pkg:mason/') then
            return pkg
          end
        end
      end
    end
  end
  local ok_pkg, pkg = pcall(registry.get_package, name)
  if ok_pkg and pkg then
    local id = pkg.spec and pkg.spec.source and pkg.spec.source.id or ''
    if not id:match('^pkg:mason/') then
      return pkg
    end
  end
  return nil
end

a.run_blocking(function()
  local ok, result = a.wait(registry.update)
  a.scheduler()
  if not ok then
    error(('Failed to update Mason registries: %s'):format(vim.inspect(result)))
  end
  reload_sources()
  a.scheduler()

  local failed = {}
  for _, name in ipairs(names) do
    local pkg = find_package(name)
    if not pkg then
      failed[#failed + 1] = name .. ': not found'
    else
      local ok_uninstall, uninstall_result = a.wait(function(resolve, reject)
        pkg:uninstall({}, function(success, result)
          if success then
            resolve(result)
          else
            reject(result)
          end
        end)
      end)
      a.scheduler()
      if ok_uninstall then
        vim.api.nvim_out_write(('MASON_REMOVED:%s\n'):format(pkg.name or name))
      else
        failed[#failed + 1] = name .. ': ' .. vim.inspect(uninstall_result)
      end
    end
  end
  if #failed > 0 then
    error(table.concat(failed, '; '))
  end
end)
`
	lua = masonLocateLua() + lua
	lua = strings.ReplaceAll(lua, "__BLOOM_NAMES__", luaStringArray(names))
	return lua
}
