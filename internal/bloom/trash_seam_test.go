package bloom

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Tests move into BLOOM_TEST_TRASH_DIR instead of the real Trash.
func init() {
	honorTestTrashDir = true
}

// Outside tests BLOOM_TEST_TRASH_DIR is ignored: a move always lands in the
// user's own Trash, never in a folder an exported variable names.
func TestTrashMoveIgnoresTheTestTrashOutsideTests(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	redirect := filepath.Join(home, "redirect")
	t.Setenv("BLOOM_TEST_TRASH_DIR", redirect)
	honorTestTrashDir = false
	t.Cleanup(func() { honorTestTrashDir = true })

	cache := filepath.Join(home, "Library", "Caches", "DropApp")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := movePathToTrash(context.Background(), &recordingRunner{outputs: map[string]CommandOutput{}}, cache); err != nil {
		t.Fatalf("move failed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".Trash", "DropApp")); err != nil {
		t.Fatalf("item did not reach the user's Trash: %v", err)
	}
	if _, err := os.Lstat(redirect); !os.IsNotExist(err) {
		t.Fatalf("BLOOM_TEST_TRASH_DIR was used outside tests: %v", err)
	}
}
