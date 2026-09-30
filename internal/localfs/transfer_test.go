package localfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func put(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0640); err != nil {
		t.Fatal(err)
	}
}
func content(t *testing.T, p, want string) {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil || string(b) != want {
		t.Fatalf("%s: %q %v, want %q", p, b, e, want)
	}
}
func TestMergePolicies(t *testing.T) {
	for _, mode := range []Conflict{Skip, Rename, Replace} {
		for _, move := range []bool{false, true} {
			t.Run(string(mode)+map[bool]string{true: "-move", false: "-copy"}[move], func(t *testing.T) {
				root := t.TempDir()
				src := filepath.Join(root, "source")
				dst := filepath.Join(root, "destination")
				put(t, src+"/nested/common.txt", "new")
				put(t, src+"/unique.txt", "unique")
				put(t, dst+"/nested/common.txt", "old")
				put(t, dst+"/untouched.txt", "keep")
				_, err := (&Engine{}).Transfer(context.Background(), src, dst, move, mode, "")
				if err != nil {
					t.Fatal(err)
				}
				content(t, dst+"/untouched.txt", "keep")
				content(t, dst+"/unique.txt", "unique")
				if mode == Replace {
					content(t, dst+"/nested/common.txt", "new")
				} else {
					content(t, dst+"/nested/common.txt", "old")
				}
				if mode == Rename {
					content(t, dst+"/nested/common (1).txt", "new")
				}
				if move && mode == Skip {
					content(t, src+"/nested/common.txt", "new")
				}
				if !move {
					content(t, src+"/unique.txt", "unique")
				}
			})
		}
	}
}
func TestCancellationPreservesSource(t *testing.T) {
	root := t.TempDir()
	src := root + "/source"
	dst := root + "/destination"
	put(t, src, "original")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&Engine{}).Transfer(ctx, src, dst, false, Ask, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	content(t, src, "original")
	if _, err = os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("destination exists")
	}
}
func TestLinksCannotRedirectDestination(t *testing.T) {
	root := t.TempDir()
	put(t, root+"/source", "new")
	put(t, root+"/outside/file", "old")
	os.Symlink(root+"/outside", root+"/link")
	_, err := (&Engine{}).Transfer(context.Background(), root+"/source", root+"/link/file", false, Replace, "")
	if err == nil {
		t.Fatal("followed parent link")
	}
	content(t, root+"/outside/file", "old")
}
func TestChangedDestinationPreserved(t *testing.T) {
	root := t.TempDir()
	put(t, root+"/source", "new")
	put(t, root+"/dest", "old")
	rev, _ := Revision(root + "/dest")
	put(t, root+"/dest", "concurrent edit")
	_, err := (&Engine{}).Transfer(context.Background(), root+"/source", root+"/dest", false, Replace, rev)
	if err == nil {
		t.Fatal("replaced changed destination")
	}
	content(t, root+"/dest", "concurrent edit")
}
func TestTypeConflictPreserved(t *testing.T) {
	root := t.TempDir()
	put(t, root+"/source/file", "new")
	put(t, root+"/dest", "old")
	_, err := (&Engine{}).Transfer(context.Background(), root+"/source", root+"/dest", false, Replace, "")
	if err == nil {
		t.Fatal("replaced another type")
	}
	content(t, root+"/dest", "old")
}
func TestNoStagingAfterCopy(t *testing.T) {
	root := t.TempDir()
	put(t, root+"/source", "new")
	_, err := (&Engine{}).Transfer(context.Background(), root+"/source", root+"/dest", false, Ask, "")
	if err != nil {
		t.Fatal(err)
	}
	items, _ := os.ReadDir(root)
	if len(items) != 2 {
		t.Fatalf("staging retained: %v", items)
	}
}
