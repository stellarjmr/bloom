package bloom

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sync"
)

type Runner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) CommandOutput
}

type InteractiveRunner interface {
	RunInteractive(ctx context.Context, name string, args ...string) CommandOutput
}

type CommandOutput struct {
	Stdout string
	Stderr string
	Err    error
}

func (o CommandOutput) Combined() string {
	if o.Stderr == "" {
		return o.Stdout
	}
	if o.Stdout == "" {
		return o.Stderr
	}
	return o.Stdout + "\n" + o.Stderr
}

type OSRunner struct{}

func (OSRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (OSRunner) Run(ctx context.Context, name string, args ...string) CommandOutput {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return CommandOutput{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Err:    err,
	}
}

func (OSRunner) RunInteractive(ctx context.Context, name string, args ...string) CommandOutput {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return CommandOutput{Err: cmd.Run()}
}

type cachedRunner struct {
	runner  Runner
	mu      sync.Mutex
	lookups map[string]cachedLookup
}

type cachedLookup struct {
	path string
	err  error
}

func newCachedRunner(runner Runner) *cachedRunner {
	return &cachedRunner{
		runner:  runner,
		lookups: map[string]cachedLookup{},
	}
}

func (r *cachedRunner) LookPath(file string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if result, ok := r.lookups[file]; ok {
		return result.path, result.err
	}
	path, err := r.runner.LookPath(file)
	r.lookups[file] = cachedLookup{path: path, err: err}
	return path, err
}

func (r *cachedRunner) Run(ctx context.Context, name string, args ...string) CommandOutput {
	return r.runner.Run(ctx, name, args...)
}

func (r *cachedRunner) RunInteractive(ctx context.Context, name string, args ...string) CommandOutput {
	if interactive, ok := r.runner.(InteractiveRunner); ok {
		return interactive.RunInteractive(ctx, name, args...)
	}
	return r.runner.Run(ctx, name, args...)
}

func runInteractive(ctx context.Context, runner Runner, name string, args ...string) CommandOutput {
	if interactive, ok := runner.(InteractiveRunner); ok {
		return interactive.RunInteractive(ctx, name, args...)
	}
	return runner.Run(ctx, name, args...)
}
