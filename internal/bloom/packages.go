package bloom

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

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
