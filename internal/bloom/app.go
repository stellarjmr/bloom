package bloom

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var Version = "dev"

func Main(args []string, stdout, stderr io.Writer) int {
	app := &App{
		Out:    stdout,
		Err:    stderr,
		Runner: OSRunner{},
	}
	return app.Run(args)
}

type App struct {
	Out    io.Writer
	Err    io.Writer
	Runner Runner
}

func (a *App) Run(args []string) int {
	if len(args) == 0 {
		a.printHelp()
		return 0
	}

	switch args[0] {
	case "-h", "--help", "help":
		a.printHelp()
		return 0
	case "-v", "--version", "version":
		fmt.Fprintf(a.Out, "bm %s\n", Version)
		return 0
	case "config":
		return a.runConfig(args[1:])
	case "clean":
		return a.runClean(args[1:])
	case "list":
		return a.runList(args[1:])
	case "doctor":
		return a.runDoctor(args[1:])
	case "check":
		return a.runCheck(args[1:])
	case "remove":
		return a.runRemove(args[1:])
	case "update":
		return a.runUpdate(args[1:])
	case "uninstall":
		return a.runUninstall(args[1:])
	default:
		fmt.Fprintf(a.Err, "unknown command: %s\n\n", args[0])
		a.printHelp()
		return 2
	}
}

func (a *App) printHelp() {
	fmt.Fprintln(a.Out, `bm updates developer tools from one terminal command.

Usage:
  bm check [--format tree|tsv] [--config path]
  bm clean [--dry-run] [--whitelist] [--config path]
  bm remove [--list] [--dry-run] [--package task:package]...
  bm update [--dry-run] [--only task] [--skip task] [--package task:package] [--config path]
  bm uninstall [--list] [--dry-run] [--app /path/to/App.app]...
  bm list [--config path]
  bm doctor [--config path]
  bm config
  bm config path
  bm config init [--force]
  bm --version

Tasks:
  brew, cask, amp, yazi, nvim, mason, npm`)
}

func (a *App) runConfig(args []string) int {
	if len(args) == 0 {
		a.printConfigHelp()
		return 0
	}
	if len(args) == 1 && args[0] == "path" {
		fmt.Fprintln(a.Out, DefaultConfigPath())
		return 0
	}
	if len(args) >= 1 && args[0] == "init" {
		fs := flag.NewFlagSet("config init", flag.ContinueOnError)
		fs.SetOutput(a.Err)
		force := fs.Bool("force", false, "overwrite existing config")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		path := DefaultConfigPath()
		if err := WriteDefaultConfig(path, *force); err != nil {
			fmt.Fprintf(a.Err, "config init failed: %v\n", err)
			return 1
		}
		fmt.Fprintln(a.Out, path)
		return 0
	}
	switch args[0] {
	case "tasks":
		return a.runConfigTasks(args[1:])
	case "packages":
		return a.runConfigPackages(args[1:])
	case "include":
		return a.runConfigInclude(args[1:])
	case "clean-whitelist":
		return a.runConfigCleanWhitelist(args[1:])
	case "clean-whitelist-items":
		return a.runConfigCleanWhitelistItems(args[1:])
	case "set-tasks":
		return a.runConfigSetTasks(args[1:])
	case "set-include":
		return a.runConfigSetInclude(args[1:])
	case "set-clean-whitelist":
		return a.runConfigSetCleanWhitelist(args[1:])
	case "reset":
		return a.runConfigReset(args[1:])
	}
	fmt.Fprintf(a.Err, "unknown config command: %s\n\n", args[0])
	a.printConfigHelp()
	return 2
}

func (a *App) printConfigHelp() {
	fmt.Fprintln(a.Out, `Usage:
  bm config path
  bm config init [--force]
  bm config tasks [--config path]
  bm config packages task
  bm config include [--config path] task
  bm config clean-whitelist [--config path]
  bm config clean-whitelist-items
  bm config set-tasks [--config path] task...
  bm config set-include [--config path] task package...
  bm config set-clean-whitelist [--config path] pattern...
  bm config reset [--config path]`)
}

func (a *App) runConfigTasks(args []string) int {
	fs := flag.NewFlagSet("config tasks", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := LoadConfig(resolveConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}
	descriptions, err := defaultTaskDescriptions()
	if err != nil {
		fmt.Fprintf(a.Err, "task error: %v\n", err)
		return 1
	}
	for _, name := range DefaultTaskNames() {
		enabled := cfg.Tasks[name].Enabled && containsString(cfg.TaskOrder, name)
		fmt.Fprintf(a.Out, "%s\t%s\t%s\n", name, formatBool(enabled), descriptions[name])
	}
	return 0
}

func (a *App) runConfigPackages(args []string) int {
	fs := flag.NewFlagSet("config packages", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Err, "usage: bm config packages task")
		return 2
	}
	packages, err := ListTaskPackages(context.Background(), a.Runner, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(a.Err, "package error: %v\n", err)
		return 1
	}
	for _, name := range packages {
		fmt.Fprintln(a.Out, name)
	}
	return 0
}

func (a *App) runConfigInclude(args []string) int {
	fs := flag.NewFlagSet("config include", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.Err, "usage: bm config include task")
		return 2
	}
	cfg, err := LoadConfig(resolveConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}
	for _, name := range cfg.Tasks[fs.Arg(0)].Include {
		fmt.Fprintln(a.Out, name)
	}
	return 0
}

func (a *App) runConfigCleanWhitelist(args []string) int {
	fs := flag.NewFlagSet("config clean-whitelist", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Err, "usage: bm config clean-whitelist")
		return 2
	}
	cfg, err := LoadConfig(resolveConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}
	for _, pattern := range cfg.Clean.Whitelist {
		fmt.Fprintln(a.Out, pattern)
	}
	return 0
}

func (a *App) runConfigCleanWhitelistItems(args []string) int {
	fs := flag.NewFlagSet("config clean-whitelist-items", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Err, "usage: bm config clean-whitelist-items")
		return 2
	}
	for _, item := range CleanWhitelistItems() {
		fmt.Fprintf(a.Out, "%s\t%s\t%s\n", item.Label, item.Pattern, item.Category)
	}
	return 0
}

func (a *App) runConfigSetTasks(args []string) int {
	fs := flag.NewFlagSet("config set-tasks", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path := resolveConfigPath(*configPath)
	cfg, err := LoadConfig(path)
	if err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}
	if err := SetEnabledTasks(&cfg, fs.Args()); err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}
	if err := SaveConfig(path, cfg); err != nil {
		fmt.Fprintf(a.Err, "config write failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Out, path)
	return 0
}

func (a *App) runConfigSetInclude(args []string) int {
	fs := flag.NewFlagSet("config set-include", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(a.Err, "usage: bm config set-include task package...")
		return 2
	}
	path := resolveConfigPath(*configPath)
	cfg, err := LoadConfig(path)
	if err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}
	if err := SetTaskInclude(&cfg, fs.Arg(0), fs.Args()[1:]); err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}
	if err := SaveConfig(path, cfg); err != nil {
		fmt.Fprintf(a.Err, "config write failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Out, path)
	return 0
}

func (a *App) runConfigSetCleanWhitelist(args []string) int {
	fs := flag.NewFlagSet("config set-clean-whitelist", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path := resolveConfigPath(*configPath)
	cfg, err := LoadConfig(path)
	if err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}
	if err := SetCleanWhitelist(&cfg, fs.Args()); err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}
	if err := SaveConfig(path, cfg); err != nil {
		fmt.Fprintf(a.Err, "config write failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Out, path)
	return 0
}

func (a *App) runConfigReset(args []string) int {
	fs := flag.NewFlagSet("config reset", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path := resolveConfigPath(*configPath)
	if err := SaveConfig(path, DefaultConfig()); err != nil {
		fmt.Fprintf(a.Err, "config write failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Out, path)
	return 0
}

func (a *App) runClean(args []string) int {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	dryRun := fs.Bool("dry-run", false, "show what would be moved to Trash")
	whitelist := fs.Bool("whitelist", false, "print active clean whitelist")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(a.Err, "unexpected clean argument: %s\n", fs.Arg(0))
		return 2
	}
	if *whitelist {
		return a.runConfigCleanWhitelist([]string{"--config", resolveConfigPath(*configPath)})
	}

	cfg, err := LoadConfig(resolveConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := RunClean(ctx, CleanOptions{DryRun: *dryRun, Config: cfg, Runner: newCachedRunner(a.Runner)})
	printCleanResult(a.Out, a.Err, res)
	if len(res.Failed) > 0 {
		return 1
	}
	return 0
}

func printCleanResult(out, errOut io.Writer, res CleanResult) {
	if res.DryRun {
		fmt.Fprintln(out, "Clean dry run - no changes made")
	} else {
		fmt.Fprintln(out, "Clean - moving files to Trash")
	}
	if len(res.Whitelist) > 0 {
		fmt.Fprintf(out, "Whitelist: %d patterns active\n", len(res.Whitelist))
	}
	if len(res.Targets) == 0 {
		fmt.Fprintln(out, "System already clean; no files moved.")
	} else {
		marker := "✓"
		if res.DryRun {
			marker = "·"
		}
		for _, target := range res.Targets {
			fmt.Fprintf(out, "%s %s  %s\n", marker, target.Label, FormatBytes(target.SizeKB))
			fmt.Fprintf(out, "   └── %s\n", target.Path)
		}
	}
	for _, failed := range res.Failed {
		fmt.Fprintf(errOut, "✗ %s: %s\n", failed.Path, failed.Reason)
	}
	if len(res.Targets) > 0 {
		verb := "Moved"
		if res.DryRun {
			verb = "Would move"
		}
		fmt.Fprintf(out, "\n%s %d items to Trash (%s)\n", verb, len(res.Targets), FormatBytes(res.TotalKB))
	}
	if len(res.Skipped) > 0 {
		fmt.Fprintf(out, "Skipped %d protected, whitelisted, invalid, or Trash items\n", len(res.Skipped))
	}
}

func (a *App) runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := LoadConfig(resolveConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}

	tasks, err := BuildTasks(cfg)
	if err != nil {
		fmt.Fprintf(a.Err, "task error: %v\n", err)
		return 1
	}

	fmt.Fprintln(a.Out, "tasks:")
	for _, task := range tasks {
		state := "enabled"
		if !task.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(a.Out, "  %-8s %-8s %s\n", task.Name, state, task.Description)
	}
	return 0
}

func (a *App) runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := LoadConfig(resolveConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}

	tasks, err := BuildTasks(cfg)
	if err != nil {
		fmt.Fprintf(a.Err, "task error: %v\n", err)
		return 1
	}

	fmt.Fprintln(a.Out, "doctor:")
	for _, task := range tasks {
		if !task.Enabled {
			fmt.Fprintf(a.Out, "  %-8s disabled\n", task.Name)
			continue
		}
		if task.RequiredCommand == "" {
			fmt.Fprintf(a.Out, "  %-8s ok\n", task.Name)
			continue
		}
		path, err := a.Runner.LookPath(task.RequiredCommand)
		if err != nil {
			fmt.Fprintf(a.Out, "  %-8s missing\n", task.Name)
			continue
		}
		fmt.Fprintf(a.Out, "  %-8s %s\n", task.Name, path)
	}
	return 0
}

func (a *App) runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	format := fs.String("format", "tree", "output format: tree or tsv")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := LoadConfig(resolveConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}

	items, err := CheckUpdates(context.Background(), newCachedRunner(a.Runner), cfg)
	if err != nil {
		fmt.Fprintf(a.Err, "check error: %v\n", err)
		return 1
	}

	switch *format {
	case "tsv":
		for _, item := range items {
			fmt.Fprintf(a.Out, "%s\t%s\n", item.Task, item.Name)
		}
	case "tree":
		if len(items) == 0 {
			fmt.Fprintln(a.Out, "no updates available")
			return 0
		}
		printSummaryGroups(a.Out, summaryGroupsFromItems(items))
	default:
		fmt.Fprintf(a.Err, "unknown check format: %s\n", *format)
		return 2
	}
	return 0
}

func (a *App) runRemove(args []string) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	listOnly := fs.Bool("list", false, "list removable packages as TSV (task, package)")
	dryRun := fs.Bool("dry-run", false, "show what would be removed without uninstalling")
	var packages multiFlag
	fs.Var(&packages, "package", "remove this task:package; repeatable")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := newCachedRunner(a.Runner)

	if *listOnly {
		items, err := ListRemovablePackages(ctx, runner)
		if err != nil {
			fmt.Fprintf(a.Err, "remove list error: %v\n", err)
			return 1
		}
		for _, item := range items {
			fmt.Fprintf(a.Out, "%s\t%s\n", item.Task, item.Name)
		}
		return 0
	}

	refs, err := parsePackageRefs(packages.Values())
	if err != nil {
		fmt.Fprintf(a.Err, "package error: %v\n", err)
		return 2
	}
	if len(refs) == 0 {
		fmt.Fprintln(a.Err, "remove requires --list or --package task:package")
		return 2
	}
	if err := validateRemoveRefs(refs); err != nil {
		fmt.Fprintf(a.Err, "package error: %v\n", err)
		return 2
	}

	results := RemovePackages(ctx, runner, refs, *dryRun)
	failures := 0
	successes := 0
	for _, res := range results {
		label := res.Package.Task + ":" + res.Package.Name
		if res.Err != nil {
			fmt.Fprintf(a.Err, "✗ %s: %v\n", label, res.Err)
			failures++
			continue
		}
		marker := "✓"
		if *dryRun {
			marker = "·"
		}
		fmt.Fprintf(a.Out, "%s %s\n", marker, label)
		successes++
	}

	if successes > 0 {
		title := "Removed"
		if *dryRun {
			title = "Would remove"
		}
		fmt.Fprintf(a.Out, "\n%s %d packages\n", title, successes)
	}
	if failures > 0 {
		return 1
	}
	return 0
}

func validateRemoveRefs(refs []PackageRef) error {
	for _, ref := range refs {
		if taskSupportsRemoval(ref.Task) {
			continue
		}
		switch ref.Task {
		case "cask":
			return fmt.Errorf("cask packages are applications; use bm uninstall")
		case "nvim":
			return fmt.Errorf("nvim plugins are managed by your Neovim config")
		default:
			return fmt.Errorf("task %q does not support package removal", ref.Task)
		}
	}
	return nil
}

func taskSupportsRemoval(name string) bool {
	switch name {
	case "brew", "yazi", "mason", "npm":
		return true
	default:
		return false
	}
}

func (a *App) runUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	dryRun := fs.Bool("dry-run", false, "show what would run without updating")
	noColor := fs.Bool("no-color", false, "disable color")
	var only multiFlag
	var skip multiFlag
	var packages multiFlag
	fs.Var(&only, "only", "run only this task; repeatable")
	fs.Var(&skip, "skip", "skip this task; repeatable")
	fs.Var(&packages, "package", "update only task:package; repeatable")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := LoadConfig(resolveConfigPath(*configPath))
	if err != nil {
		fmt.Fprintf(a.Err, "config error: %v\n", err)
		return 1
	}
	if *noColor {
		cfg.Color = false
	}
	onlyValues := only.Values()
	packageOnly, err := applyPackageFilters(&cfg, packages.Values())
	if err != nil {
		fmt.Fprintf(a.Err, "package filter error: %v\n", err)
		return 2
	}
	if len(packageOnly) > 0 && len(onlyValues) == 0 {
		onlyValues = packageOnly
	}

	tasks, err := BuildTasks(cfg)
	if err != nil {
		fmt.Fprintf(a.Err, "task error: %v\n", err)
		return 1
	}
	tasks, err = filterTasks(tasks, onlyValues, skip.Values())
	if err != nil {
		fmt.Fprintf(a.Err, "task error: %v\n", err)
		return 1
	}
	runner := newCachedRunner(a.Runner)
	tasks = filterRunnableTasks(tasks, runner)
	if len(tasks) == 0 {
		fmt.Fprintln(a.Out, "no available tasks selected")
		return 0
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := UpdateOptions{DryRun: *dryRun, Config: cfg}
	results := make([]TaskResult, 0, len(tasks))
	progress := NewProgress(a.Out, cfg)

	for i, task := range tasks {
		if !task.Enabled {
			res := TaskResult{Name: task.Name, Status: StatusSkipped, Message: "disabled"}
			results = append(results, res)
			progress.Render(i+1, len(tasks), res)
			continue
		}

		stopProgress := progress.Animate(i, len(tasks), TaskResult{Name: task.Name, Status: StatusRunning})
		start := time.Now()
		res := task.Run(ctx, runner, opts)
		stopProgress()
		res.Name = task.Name
		res.Duration = time.Since(start)
		results = append(results, res)
		progress.Render(i+1, len(tasks), res)
		if res.Err != nil {
			progress.Finish()
			fmt.Fprintf(a.Err, "%s failed: %v\n", task.Name, res.Err)
			if res.Output != "" {
				fmt.Fprintln(a.Err, strings.TrimSpace(res.Output))
			}
		} else if res.Output != "" {
			progress.Finish()
			fmt.Fprintf(a.Err, "%s: %s\n", task.Name, strings.TrimSpace(res.Output))
		}
	}

	failures := 0
	for _, res := range results {
		if res.Err != nil {
			failures++
		}
	}
	if failures == 0 {
		progress.Render(len(tasks), len(tasks), TaskResult{Name: "done!", Status: StatusOK})
	}
	progress.Finish()

	printSummaryGroups(a.Out, summaryGroupsFromResults(results))

	if failures > 0 {
		return 1
	}
	return 0
}

func (a *App) runUninstall(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	listOnly := fs.Bool("list", false, "list installed apps as TSV (path, name, bundleID, sizeKB)")
	dryRun := fs.Bool("dry-run", false, "show what would be removed without deleting")
	var apps multiFlag
	fs.Var(&apps, "app", "uninstall this app path; repeatable")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if *listOnly {
		entries, err := ScanApplications(ctx)
		if err != nil {
			fmt.Fprintf(a.Err, "scan error: %v\n", err)
			return 1
		}
		PrintAppList(a.Out, entries)
		return 0
	}

	values := apps.Values()
	if len(values) == 0 {
		fmt.Fprintln(a.Err, "uninstall requires --list or --app /path/to/App.app")
		return 2
	}

	known, err := ScanApplications(ctx)
	if err != nil {
		fmt.Fprintf(a.Err, "scan error: %v\n", err)
		return 1
	}
	byPath := make(map[string]AppEntry, len(known))
	for _, app := range known {
		byPath[app.Path] = app
	}

	failures := 0
	runner := newCachedRunner(a.Runner)

	targets := make([]AppEntry, 0, len(values))
	for _, path := range values {
		entry, ok := byPath[path]
		if !ok {
			entry = AppEntry{
				Path:     path,
				Name:     filepath.Base(strings.TrimSuffix(strings.TrimRight(path, "/"), ".app")),
				BundleID: readBundleID(path),
				SizeKB:   directorySizeKB(path),
			}
		}
		if _, err := os.Stat(entry.Path); err != nil {
			fmt.Fprintf(a.Err, "✗ %s: not found\n", entry.Path)
			failures++
			continue
		}
		if isProtectedAppPath(entry.Path) {
			fmt.Fprintf(a.Err, "✗ %s: protected system app\n", entry.Path)
			failures++
			continue
		}
		targets = append(targets, entry)
	}

	if len(targets) == 0 {
		if failures > 0 {
			return 1
		}
		return 0
	}

	if *dryRun {
		fmt.Fprintln(a.Out, "Would move the following items to Trash:")
		summary := BatchUninstall(ctx, runner, targets, true)
		_, printFailures := a.printUninstallSummary(summary, true)
		failures += printFailures
	} else {
		fmt.Fprintln(a.Out, "The following items will be moved to Trash:")
		plan := BatchUninstall(ctx, runner, targets, true)
		planned, printFailures := a.printUninstallSummary(plan, true)
		failures += printFailures
		if planned == 0 {
			if failures > 0 {
				return 1
			}
			return 0
		}
		if !confirmUninstall(os.Stdin, a.Out) {
			fmt.Fprintln(a.Out, "Canceled")
			if failures > 0 {
				return 1
			}
			return 0
		}

		fmt.Fprintln(a.Out)
		summary := BatchUninstall(ctx, runner, targets, false)
		_, printFailures = a.printUninstallSummary(summary, false)
		failures += printFailures
	}

	if failures > 0 {
		return 1
	}
	return 0
}

func (a *App) printUninstallSummary(summary BatchSummary, dryRun bool) (int, int) {
	processed := 0
	failures := 0
	for _, res := range summary.Results {
		if res.Err != nil {
			fmt.Fprintf(a.Err, "✗ %s: %v\n", res.App.Name, res.Err)
			failures++
			continue
		}
		marker := "✓"
		if dryRun {
			marker = "·"
		}
		brewNote := ""
		if res.BrewRemoved {
			brewNote = "  [brew cask]"
		}
		fmt.Fprintf(a.Out, "%s %s  %s  (%d files)%s\n", marker, res.App.Name, FormatBytes(res.RemovedKB), len(res.Files), brewNote)
		fileMark := "✓"
		if dryRun {
			fileMark = "·"
		}
		for _, p := range res.Files {
			fmt.Fprintf(a.Out, "   %s %s\n", fileMark, p)
		}
		for _, p := range res.Failed {
			fmt.Fprintf(a.Err, "   ! could not move to Trash %s\n", p)
		}
		processed++
	}

	if processed > 0 {
		if dryRun {
			fmt.Fprintf(a.Out, "\nWould move %d apps to Trash, total %s\n", processed, FormatBytes(summary.TotalRemovedKB))
		} else {
			fmt.Fprintf(a.Out, "\nUninstalled %d apps, moved %s to Trash\n", processed, FormatBytes(summary.TotalRemovedKB))
		}
		if summary.BrewAutoremove {
			fmt.Fprintln(a.Out, "ran brew autoremove")
		}
	}
	return processed, failures
}

func confirmUninstall(in io.Reader, out io.Writer) bool {
	fmt.Fprintln(out)
	fmt.Fprint(out, "Press Enter to move these items to Trash, or Q to cancel: ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && answer == "" {
		fmt.Fprintln(out)
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "" || answer == "y" || answer == "yes"
}

func filterRunnableTasks(tasks []Task, runner Runner) []Task {
	filtered := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		if !task.Enabled {
			continue
		}
		if task.RequiredCommand != "" {
			if _, err := runner.LookPath(task.RequiredCommand); err != nil {
				continue
			}
		}
		filtered = append(filtered, task)
	}
	return filtered
}

type summaryGroup struct {
	Name  string
	Items []string
}

func summaryGroupsFromResults(results []TaskResult) []summaryGroup {
	groups := make([]summaryGroup, 0, len(results))
	for _, res := range results {
		if len(res.Summary) == 0 {
			continue
		}
		groups = append(groups, summaryGroup{Name: res.Name, Items: res.Summary})
	}
	return groups
}

func summaryGroupsFromItems(items []UpdateItem) []summaryGroup {
	groups := []summaryGroup{}
	groupIndex := map[string]int{}
	for _, item := range items {
		idx, ok := groupIndex[item.Task]
		if !ok {
			idx = len(groups)
			groupIndex[item.Task] = idx
			groups = append(groups, summaryGroup{Name: item.Task})
		}
		groups[idx].Items = append(groups[idx].Items, item.Name)
	}
	return groups
}

func printSummaryGroups(out io.Writer, groups []summaryGroup) {
	for _, group := range groups {
		if len(group.Items) == 0 {
			continue
		}
		fmt.Fprintf(out, "✓ %s\n", group.Name)
		for i, item := range group.Items {
			branch := "├──"
			if i == len(group.Items)-1 {
				branch = "└──"
			}
			fmt.Fprintf(out, "   %s %s\n", branch, item)
		}
	}
}

func defaultTaskDescriptions() (map[string]string, error) {
	tasks, err := BuildTasks(DefaultConfig())
	if err != nil {
		return nil, err
	}
	descriptions := map[string]string{}
	for _, task := range tasks {
		descriptions[task.Name] = task.Description
	}
	return descriptions, nil
}

func filterTasks(tasks []Task, only, skip []string) ([]Task, error) {
	taskNames := map[string]bool{}
	for _, task := range tasks {
		taskNames[task.Name] = true
	}

	onlySet := map[string]bool{}
	for _, name := range only {
		if !taskNames[name] {
			return nil, fmt.Errorf("unknown --only task %q", name)
		}
		onlySet[name] = true
	}

	skipSet := map[string]bool{}
	for _, name := range skip {
		if !taskNames[name] {
			return nil, fmt.Errorf("unknown --skip task %q", name)
		}
		skipSet[name] = true
	}

	filtered := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		if len(onlySet) > 0 && !onlySet[task.Name] {
			continue
		}
		if skipSet[task.Name] {
			continue
		}
		filtered = append(filtered, task)
	}
	return filtered, nil
}

func applyPackageFilters(cfg *Config, filters []string) ([]string, error) {
	refs, err := parsePackageRefs(filters)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	if cfg.Tasks == nil {
		cfg.Tasks = map[string]TaskConfig{}
	}
	byTask := map[string][]string{}
	for _, ref := range refs {
		byTask[ref.Task] = append(byTask[ref.Task], ref.Name)
	}

	only := make([]string, 0, len(byTask))
	for _, task := range cfg.TaskOrder {
		names := byTask[task]
		if len(names) == 0 {
			continue
		}
		taskCfg := cfg.Tasks[task]
		taskCfg.Include = uniqueStrings(names)
		taskCfg.Exclude = nil
		cfg.Tasks[task] = taskCfg
		only = append(only, task)
	}
	return only, nil
}

func parsePackageRefs(filters []string) ([]PackageRef, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	refs := make([]PackageRef, 0, len(filters))
	seen := map[string]bool{}
	for _, filter := range filters {
		task, name, ok := strings.Cut(filter, ":")
		task = strings.TrimSpace(task)
		name = strings.TrimSpace(name)
		if !ok || task == "" || name == "" {
			return nil, fmt.Errorf("expected task:package, got %q", filter)
		}
		if !isDefaultTask(task) {
			return nil, fmt.Errorf("unknown task %q", task)
		}
		if !taskSupportsPackages(task) {
			return nil, fmt.Errorf("task %q does not support package filters", task)
		}
		key := task + "\x00" + name
		if seen[key] {
			continue
		}
		seen[key] = true
		refs = append(refs, PackageRef{Task: task, Name: name})
	}
	return refs, nil
}

func resolveConfigPath(path string) string {
	if path != "" {
		return path
	}
	return DefaultConfigPath()
}

type multiFlag []string

func (f *multiFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *multiFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("empty value")
	}
	*f = append(*f, value)
	return nil
}

func (f *multiFlag) Values() []string {
	return append([]string(nil), *f...)
}

func configHome() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home + "/.config"
}
