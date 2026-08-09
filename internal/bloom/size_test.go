package bloom

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type diskUsageRunner struct {
	mdls    CommandOutput
	du      CommandOutput
	blockDu bool
	calls   []string
}

func (r *diskUsageRunner) LookPath(file string) (string, error) {
	return "/usr/bin/" + file, nil
}

func (r *diskUsageRunner) Run(ctx context.Context, name string, args ...string) CommandOutput {
	r.calls = append(r.calls, filepath.Base(name)+" "+strings.Join(args, " "))
	switch filepath.Base(name) {
	case "mdls":
		return r.mdls
	case "du":
		if r.blockDu {
			<-ctx.Done()
			return CommandOutput{Err: ctx.Err()}
		}
		return r.du
	default:
		return CommandOutput{Err: errors.New("unexpected command")}
	}
}

func TestPathSizeUsesPhysicalBlocksForRegularFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparse.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(64 << 20); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("allocated block count unavailable")
	}
	want := (int64(stat.Blocks) + 1) / 2

	got, err := pathSizeKB(context.Background(), &diskUsageRunner{}, path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("physical size = %dKB, want %dKB from st_blocks", got, want)
	}
	if got >= (64 << 10) {
		t.Fatalf("sparse file was reported by logical size: %dKB", got)
	}
}

func TestPathSizeUsesPhysicalSpotlightMetadataForApps(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Sized.app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &diskUsageRunner{mdls: CommandOutput{Stdout: "4096\n"}}

	got, err := pathSizeKB(context.Background(), runner, app)
	if err != nil {
		t.Fatal(err)
	}
	if got != 4 {
		t.Fatalf("physical app size = %dKB, want 4KB", got)
	}
	if len(runner.calls) != 1 || !strings.Contains(runner.calls[0], "kMDItemPhysicalSize") {
		t.Fatalf("size commands = %#v, want one physical mdls query", runner.calls)
	}
}

func TestPathSizeRejectsPartialDiskUsageOutput(t *testing.T) {
	dir := t.TempDir()
	runner := &diskUsageRunner{du: CommandOutput{
		Stdout: "777\t" + dir + "\n",
		Err:    errors.New("unreadable child"),
	}}

	got, err := pathSizeKB(context.Background(), runner, dir)
	if err == nil {
		t.Fatalf("partial du output succeeded with %dKB", got)
	}
	if got != 0 {
		t.Fatalf("partial du output returned %dKB, want 0", got)
	}
}

func TestPathSizeTimesOutBoundedDiskUsageProbe(t *testing.T) {
	runner := &diskUsageRunner{blockDu: true}
	started := time.Now()
	got, err := pathSizeKBWithTimeout(context.Background(), runner, t.TempDir(), 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error = %v, size = %d", err, got)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded size probe took %s", elapsed)
	}
}

type cleanSizeFailureRunner struct{}

func (cleanSizeFailureRunner) LookPath(file string) (string, error) {
	if file == "ps" {
		return "/bin/ps", nil
	}
	return "", os.ErrNotExist
}

func (cleanSizeFailureRunner) Run(_ context.Context, name string, args ...string) CommandOutput {
	switch filepath.Base(name) {
	case "ps":
		return CommandOutput{Stdout: "/sbin/launchd\n"}
	case "du":
		path := args[len(args)-1]
		if strings.Contains(path, "BadCache") {
			return CommandOutput{Stdout: "777\t" + path + "\n", Err: errors.New("unreadable child")}
		}
		return CommandOutput{Stdout: "8\t" + path + "\n"}
	default:
		return CommandOutput{Err: errors.New("unexpected command")}
	}
}

func TestRunCleanContinuesAfterOneSizeProbeFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bad := filepath.Join(home, "Library", "Caches", "BadCache")
	good := filepath.Join(home, "Library", "Caches", "GoodCache")
	for _, path := range []string{bad, good} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "cache.bin"), []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := DefaultConfig()
	cfg.Clean.Whitelist = nil

	res := RunClean(context.Background(), CleanOptions{
		DryRun: true,
		Config: cfg,
		Runner: cleanSizeFailureRunner{},
	})
	if !cleanResultContains(res, good) {
		t.Fatalf("good target was lost after sibling size failure: %#v", res.Targets)
	}
	if cleanResultContains(res, bad) {
		t.Fatalf("partially measured target appeared in result: %#v", res.Targets)
	}
	foundFailure := false
	for _, failed := range res.Failed {
		if filepath.Clean(failed.Path) == bad && strings.Contains(failed.Reason, "size unavailable") {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatalf("size failure missing from result: %#v", res.Failed)
	}
}
