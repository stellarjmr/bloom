package bloom

import (
	"bytes"
	"context"
	"os/exec"
	"sync"
)

type Runner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) CommandOutput
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
