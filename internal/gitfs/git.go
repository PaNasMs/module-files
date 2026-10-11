package gitfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type State struct {
	Root     string            `json:"root"`
	Branch   string            `json:"branch"`
	Head     string            `json:"head"`
	Branches []string          `json:"branches"`
	Changes  map[string]string `json:"changes"`
	Dirty    bool              `json:"dirty"`
	Detached bool              `json:"detached"`
}

func Root(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("invalid file path")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if real != path {
		return "", errors.New("Git does not follow symbolic links")
	}
	for dir := path; ; dir = filepath.Dir(dir) {
		if s, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			if !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
				return "", errors.New("Linked Git worktrees are not supported yet")
			}
			if head, e := os.Lstat(filepath.Join(dir, ".git", "HEAD")); e != nil || !head.Mode().IsRegular() {
				return "", nil
			}
			return dir, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if dir == "/" {
			return "", nil
		}
	}
}

func lock(root string, exclusive bool) (*os.File, error) {
	f, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	mode := unix.LOCK_SH
	if exclusive {
		mode = unix.LOCK_EX
	}
	if err = unix.Flock(int(f.Fd()), mode|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("Repository is busy; wait for synchronization to finish")
	}
	return f, nil
}

type bounded struct {
	bytes.Buffer
	full bool
}

func (b *bounded) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > 4<<20 {
		b.full = true
		return 0, errors.New("Git result is too large")
	}
	return b.Buffer.Write(p)
}
func run(ctx context.Context, root string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	options := []string{"--git-dir=" + filepath.Join(root, ".git"), "--work-tree=" + root, "-c", "core.bare=false", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "submodule.recurse=false", "-c", "core.attributesFile=/dev/null"}
	cmd := exec.CommandContext(ctx, "git", append(options, args...)...)
	cmd.Dir = root
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_LFS_SKIP_SMUDGE=1", "LC_ALL=C"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var out bounded
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("Git command failed (%s)", args[0])
	}
	if out.full {
		return nil, errors.New("Git result is too large")
	}
	return out.Bytes(), nil
}
func inspect(ctx context.Context, root string) (*State, error) {
	s := &State{Root: root, Branches: []string{}, Changes: map[string]string{}}
	// Filters can execute repository-configured commands even during status.
	filters, err := run(ctx, root, "config", "--null", "--list")
	if err != nil {
		return nil, err
	}
	for _, item := range strings.Split(string(filters), "\x00") {
		key, _, _ := strings.Cut(item, "\n")
		if strings.HasPrefix(key, "filter.") {
			return nil, errors.New("Repositories with custom Git filters require the terminal")
		}
	}
	head, err := run(ctx, root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return nil, errors.New("Repository has no commits yet")
	}
	s.Head = strings.TrimSpace(string(head))
	branch, err := run(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	s.Detached = err != nil
	s.Branch = strings.TrimSpace(string(branch))
	refs, err := run(ctx, root, "for-each-ref", "--format=%(refname)", "refs/heads/", "refs/remotes/origin/")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, ref := range strings.Fields(string(refs)) {
		name := strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/origin/")
		if name != "HEAD" && !seen[name] {
			seen[name] = true
			s.Branches = append(s.Branches, name)
		}
	}
	raw, err := run(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignore-submodules=all")
	if err != nil {
		return nil, err
	}
	records := bytes.Split(raw, []byte{0})
	for i := 0; i < len(records); i++ {
		line := records[i]
		if len(line) < 4 {
			continue
		}
		xy := string(line[:2])
		name := string(line[3:])
		status := "modified"
		switch {
		case strings.Contains(xy, "U") || xy == "AA" || xy == "DD":
			status = "conflict"
		case xy == "??":
			status = "untracked"
		case strings.Contains(xy, "D"):
			status = "deleted"
		case strings.Contains(xy, "R"):
			status = "renamed"
		case strings.Contains(xy, "A"):
			status = "added"
		}
		s.Changes[name] = status
		s.Dirty = true
		if strings.ContainsAny(xy, "RC") {
			i++
		}
	}
	return s, nil
}
func Inspect(ctx context.Context, path string) (*State, error) {
	root, err := Root(path)
	if err != nil || root == "" {
		return nil, err
	}
	f, err := lock(root, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return inspect(ctx, root)
}
func Switch(ctx context.Context, path, branch, expected string, apply bool) (*State, error) {
	root, err := Root(path)
	if err != nil {
		return nil, err
	}
	if root == "" {
		return nil, errors.New("Not a Git repository")
	}
	f, err := lock(root, true)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := inspect(ctx, root)
	if err != nil {
		return nil, err
	}
	if s.Dirty {
		return nil, errors.New("Commit or move local changes before switching branches")
	}
	if expected != "" && expected != s.Head+":"+s.Branch {
		return nil, errors.New("Repository changed; refresh and try again")
	}
	found := false
	for _, b := range s.Branches {
		found = found || b == branch
	}
	if !found || strings.HasPrefix(branch, "-") {
		return nil, errors.New("Choose an existing branch")
	}
	if !apply {
		return s, nil
	}
	if _, err = run(ctx, root, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err = run(ctx, root, "switch", "--no-guess", branch)
	} else {
		_, err = run(ctx, root, "switch", "--track", "-c", branch, "origin/"+branch)
	}
	if err != nil {
		return nil, errors.New("Could not switch branch; local files were not forcibly overwritten")
	}
	return inspect(ctx, root)
}
