package localfs

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
)

// Pin each directory without following links, including intermediate components.
// All mutations use these descriptors, so renaming an ancestor cannot redirect them.
type target struct {
	parent *os.File
	name   string
}

func (t target) close() { t.parent.Close() }
func resolve(path string) (target, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.ContainsRune(path, 0) {
		return target{}, errors.New("invalid file path")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return target{}, err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return target{}, e
		}
		fd = next
	}
	return target{os.NewFile(uintptr(fd), filepath.Dir(path)), parts[len(parts)-1]}, nil
}
func (t target) stat() (unix.Stat_t, error) {
	var s unix.Stat_t
	e := unix.Fstatat(int(t.parent.Fd()), t.name, &s, unix.AT_SYMLINK_NOFOLLOW)
	return s, e
}
func (t target) open(flags int, mode uint32) (*os.File, error) {
	fd, e := unix.Openat(int(t.parent.Fd()), t.name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, mode)
	if e != nil {
		return nil, e
	}
	return os.NewFile(uintptr(fd), filepath.Join(t.parent.Name(), t.name)), nil
}
func (t target) rename(to target, flags uint) error {
	return unix.Renameat2(int(t.parent.Fd()), t.name, int(to.parent.Fd()), to.name, flags)
}
func same(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode == b.Mode && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}
func isDir(s unix.Stat_t) bool   { return s.Mode&unix.S_IFMT == unix.S_IFDIR }
func regular(s unix.Stat_t) bool { return s.Mode&unix.S_IFMT == unix.S_IFREG }
