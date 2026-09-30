package localfs

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Entry struct {
	Name      string  `json:"name"`
	Path      string  `json:"path"`
	Directory bool    `json:"directory"`
	Link      bool    `json:"link"`
	Size      int64   `json:"size"`
	Modified  float64 `json:"modified"`
	Revision  string  `json:"revision"`
}

func directory(path string) (*os.File, error) {
	if path == "/" {
		return os.Open("/")
	}
	t, e := resolve(path)
	if e != nil {
		return nil, e
	}
	defer t.close()
	return t.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
}
func List(path string) ([]Entry, uint64, error) {
	f, err := directory(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	names, err := f.Readdirnames(10001)
	if err != nil && err != io.EOF {
		return nil, 0, err
	}
	if len(names) > 10000 {
		return nil, 0, errors.New("too many items; open a subdirectory")
	}
	entries := []Entry{}
	for _, name := range names {
		s, err := (target{f, name}).stat()
		if err != nil {
			continue
		}
		entries = append(entries, Entry{name, strings.TrimSuffix(path, "/") + "/" + name, isDir(s), s.Mode&unix.S_IFMT == unix.S_IFLNK, s.Size, float64(s.Mtim.Sec) + float64(s.Mtim.Nsec)/1e9, revision(s)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Directory != entries[j].Directory {
			return entries[i].Directory
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	var space unix.Statfs_t
	if err = unix.Fstatfs(int(f.Fd()), &space); err != nil {
		return nil, 0, err
	}
	return entries, space.Bavail * uint64(space.Bsize), nil
}
func Mkdir(path string) error {
	t, e := resolve(path)
	if e != nil {
		return e
	}
	defer t.close()
	if e = unix.Mkdirat(int(t.parent.Fd()), t.name, 0777); e != nil {
		return e
	}
	return t.parent.Sync()
}
func Delete(ctx context.Context, path string) error {
	t, e := resolve(path)
	if e != nil {
		return e
	}
	defer t.close()
	if e = removeTree(ctx, t); e != nil {
		return e
	}
	return t.parent.Sync()
}
func Open(path string) (*os.File, error) {
	t, e := resolve(path)
	if e != nil {
		return nil, e
	}
	defer t.close()
	f, e := t.open(unix.O_RDONLY, 0)
	if e != nil {
		return nil, e
	}
	s, e := f.Stat()
	if e != nil || !s.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("not a regular file")
	}
	return f, nil
}
func (e *Engine) Upload(ctx context.Context, path string, in io.Reader, size int64, expected string) error {
	if size < 0 {
		return errors.New("upload size is required")
	}
	dst, err := resolve(path)
	if err != nil {
		return err
	}
	defer dst.close()
	holder, stage, err := staging(dst)
	if err != nil {
		return err
	}
	defer holder.Close()
	preserve := false
	defer func() {
		if !preserve {
			_ = removeTree(context.Background(), target{dst.parent, filepath.Base(holder.Name())})
		}
	}()
	f, err := stage.open(unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0666)
	if err != nil {
		return err
	}
	remaining := size
	buf := make([]byte, 1<<20)
	for remaining > 0 {
		if err = e.check(ctx); err != nil {
			break
		}
		n := int64(len(buf))
		if remaining < n {
			n = remaining
		}
		var got int
		got, err = io.ReadFull(in, buf[:n])
		if err != nil {
			break
		}
		_, err = f.Write(buf[:got])
		if err != nil {
			break
		}
		remaining -= int64(got)
	}
	if err == nil {
		var b [1]byte
		n, er := in.Read(b[:])
		if n != 0 || er != io.EOF {
			err = errors.New("upload exceeds declared size")
		}
	}
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err != nil {
		return err
	}
	preserve, err = publish(stage, dst, expected)
	if err != nil {
		return err
	}
	return dst.parent.Sync()
}
func Download(path string, out io.Writer) error {
	f, err := Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return StreamFile(f, out)
}
func StreamFile(f *os.File, out io.Writer) error {
	s, err := f.Stat()
	if err != nil {
		return err
	}
	if _, err = io.CopyN(out, f, s.Size()); err != nil {
		return err
	}
	after, err := f.Stat()
	if err != nil {
		return err
	}
	if s.Size() != after.Size() || s.ModTime() != after.ModTime() {
		return errors.New("source changed during download")
	}
	return nil
}

func Trash(ctx context.Context, path, base string, engine *Engine) (string, error) {
	root, err := directory(base)
	if err != nil {
		return "", err
	}
	defer root.Close()
	name := fmt.Sprintf(".panasms-trash-%d", os.Geteuid())
	trash := target{root, name}
	if err = unix.Mkdirat(int(root.Fd()), name, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
		return "", err
	}
	f, err := trash.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var info unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &info); err != nil {
		return "", err
	}
	if info.Uid != uint32(os.Geteuid()) || info.Mode&0077 != 0 {
		return "", errors.New("invalid trash directory ownership or permissions")
	}
	var random [4]byte
	if _, err = rand.Read(random[:]); err != nil {
		return "", err
	}
	container := fmt.Sprintf("%d-%x", time.Now().UnixNano(), random)
	if err = unix.Mkdirat(int(f.Fd()), container, 0700); err != nil {
		return "", err
	}
	holder, err := (target{f, container}).open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return "", err
	}
	defer holder.Close()
	src, err := resolve(path)
	if err != nil {
		return "", err
	}
	defer src.close()
	_, err = engine.transfer(ctx, src, target{holder, src.name}, true, Ask, "")
	if err != nil {
		_ = unix.Unlinkat(int(f.Fd()), container, unix.AT_REMOVEDIR)
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	return filepath.Join(base, name, container, src.name), nil
}
