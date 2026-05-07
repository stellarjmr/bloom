package bloom

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
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
	case "list":
		return a.runList(args[1:])
	case "doctor":
		return a.runDoctor(args[1:])
	case "update":
		return a.runUpdate(args[1:])
	default:
		fmt.Fprintf(a.Err, "unknown command: %s\n\n", args[0])
		a.printHelp()
		return 2
	}
}

func (a *App) printHelp() {
	fmt.Fprintln(a.Out, `bm updates developer tools from one terminal command.

Usage:
  bm update [--dry-run] [--only task] [--skip task] [--config path]
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
	case "set-tasks":
		return a.runConfigSetTasks(args[1:])
	case "set-include":
		return a.runConfigSetInclude(args[1:])
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
  bm config set-tasks [--config path] task...
  bm config set-include [--config path] task package...
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

func (a *App) runUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	configPath := fs.String("config", "", "config file path")
	dryRun := fs.Bool("dry-run", false, "show what would run without updating")
	noColor := fs.Bool("no-color", false, "disable color")
	var only multiFlag
	var skip multiFlag
	fs.Var(&only, "only", "run only this task; repeatable")
	fs.Var(&skip, "skip", "skip this task; repeatable")
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

	tasks, err := BuildTasks(cfg)
	if err != nil {
		fmt.Fprintf(a.Err, "task error: %v\n", err)
		return 1
	}
	tasks, err = filterTasks(tasks, only.Values(), skip.Values())
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

	for _, res := range results {
		for _, line := range res.Summary {
			fmt.Fprintln(a.Out, line)
		}
	}

	if failures > 0 {
		return 1
	}
	return 0
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
