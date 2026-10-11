package gitfs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func command(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %s %v", args, b, e)
	}
}
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	command(t, dir, "init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("main"), 0600)
	command(t, dir, "add", ".")
	command(t, dir, "commit", "-m", "initial")
	command(t, dir, "switch", "-c", "other")
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("other"), 0600)
	command(t, dir, "commit", "-am", "other")
	command(t, dir, "switch", "main")
	return dir
}
func TestSwitchAndStatus(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	s, e := Inspect(ctx, dir)
	if e != nil || s.Branch != "main" || s.Dirty {
		t.Fatalf("%+v %v", s, e)
	}
	os.Mkdir(filepath.Join(dir, "sub"), 0700)
	nested, e := Inspect(ctx, filepath.Join(dir, "sub"))
	if e != nil || nested.Root != dir {
		t.Fatal(nested, e)
	}
	s, e = Switch(ctx, dir, "other", s.Head+":"+s.Branch, true)
	if e != nil || s.Branch != "other" {
		t.Fatal(s, e)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "file.txt"))
	if string(b) != "other" {
		t.Fatal(string(b))
	}
	os.WriteFile(filepath.Join(dir, "new\nfile"), []byte("new"), 0600)
	os.Remove(filepath.Join(dir, "file.txt"))
	s, e = Inspect(ctx, dir)
	if e != nil || s.Changes["new\nfile"] != "untracked" || s.Changes["file.txt"] != "deleted" {
		t.Fatal(s, e)
	}
	if _, e = Switch(ctx, dir, "main", "", true); e == nil {
		t.Fatal("dirty switch accepted")
	}
}
func TestRemoteBranchAndStaleSelection(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	command(t, dir, "update-ref", "refs/remotes/origin/remote", "other")
	command(t, dir, "config", "remote.origin.url", "https://example.invalid/r")
	command(t, dir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	s, e := Switch(ctx, dir, "remote", "", true)
	if e != nil || s.Branch != "remote" {
		t.Fatal(s, e)
	}
	if _, e = Switch(ctx, dir, "main", "stale", true); e == nil {
		t.Fatal("stale accepted")
	}
	if _, e = Switch(ctx, dir, "--discard-changes", "", true); e == nil {
		t.Fatal("option accepted")
	}
}
func TestLocksAndUnsafeConfiguration(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	f, e := lock(dir, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Switch(ctx, dir, "other", "", true); e == nil {
		t.Fatal("concurrent switch accepted")
	}
	if _, e = Inspect(ctx, dir); e == nil {
		t.Fatal("concurrent status accepted")
	}
	f.Close()
	marker := filepath.Join(dir, "executed")
	os.WriteFile(filepath.Join(dir, ".git", "hooks", "post-checkout"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700)
	if _, e = Switch(ctx, dir, "other", "", true); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("hook executed")
	}
	command(t, dir, "config", "filter.evil.clean", "touch "+marker)
	if _, e = Inspect(ctx, dir); e == nil {
		t.Fatal("custom filter accepted")
	}
}
func TestNonRepositoryAndSymlink(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Inspect(ctx, dir)
	if e != nil || s != nil {
		t.Fatal(s, e)
	}
	target := repo(t)
	link := filepath.Join(dir, "link")
	os.Symlink(target, link)
	if _, e = Inspect(ctx, link); e == nil {
		t.Fatal("symlink accepted")
	}
}
