package bloom

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the package at an empty Caskroom, so no test reads the
// host's install receipts, which would decide what a cask's zap reaches.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bloom-test-caskroom-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	caskroomDirs = []string{filepath.Join(dir, "Caskroom")}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
