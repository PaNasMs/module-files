package operations

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMkdirPlanSeparatesAccessFromConflict(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "taken"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0700) })
	plan := func(target string) error {
		_, err := Plan(context.Background(), "", Request{Action: "file.mkdir", Params: Params{Target: target}})
		return err
	}
	if err := plan(filepath.Join(locked, "evil")); err == nil || err.Error() != "permission denied" {
		t.Fatalf("inaccessible parent: %v", err)
	}
	if err := plan(filepath.Join(root, "taken")); err == nil || err.Error() != "destination already exists" {
		t.Fatalf("existing destination: %v", err)
	}
	if err := plan(filepath.Join(root, "fresh")); err != nil {
		t.Fatalf("new folder: %v", err)
	}
}
