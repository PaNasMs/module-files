package localfs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type Conflict string

const (
	Ask     Conflict = ""
	Skip    Conflict = "skip"
	Rename  Conflict = "rename"
	Replace Conflict = "replace"
)

type Result struct {
	Copied  int `json:"copied"`
	Skipped int `json:"skipped"`
}
type Engine struct {
	Check      func() error
	Progress   func(string)
	Capability func(bool)
}

func (e *Engine) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.Check != nil {
		return e.Check()
	}
	return nil
}
func (e *Engine) capability(value bool) {
	if e.Capability != nil {
		e.Capability(value)
	}
}
func revision(s unix.Stat_t) string {
	data, _ := json.Marshal([]any{s.Dev, s.Ino, s.Mode, s.Size, s.Mtim.Sec*1e9 + s.Mtim.Nsec})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func Revision(path string) (string, error) {
	t, e := resolve(path)
	if e != nil {
		return "", e
	}
	defer t.close()
	s, e := t.stat()
	return revision(s), e
}
func (e *Engine) Transfer(ctx context.Context, source, destination string, move bool, policy Conflict, expected string) (Result, error) {
	if source == destination || strings.HasPrefix(destination, source+"/") || strings.HasPrefix(source, destination+"/") {
		return Result{}, errors.New("source and destination must not contain each other")
	}
	if policy != Ask && policy != Skip && policy != Rename && policy != Replace {
		return Result{}, errors.New("invalid conflict policy")
	}
	src, err := resolve(source)
	if err != nil {
		return Result{}, err
	}
	defer src.close()
	dst, err := resolve(destination)
	if err != nil {
		return Result{}, err
	}
	defer dst.close()
	e.capability(true)
	defer e.capability(false)
	return e.transfer(ctx, src, dst, move, policy, expected)
}
func (e *Engine) transfer(ctx context.Context, src, dst target, move bool, policy Conflict, expected string) (Result, error) {
	if err := e.check(ctx); err != nil {
		return Result{}, err
	}
	before, err := src.stat()
	if err != nil {
		return Result{}, err
	}
	if !regular(before) && !isDir(before) {
		return Result{}, errors.New("select a regular file or folder")
	}
	existing, exists := dst.stat()
	if exists != nil && !errors.Is(exists, unix.ENOENT) {
		return Result{}, exists
	}
	if exists == nil {
		if before.Dev == existing.Dev && before.Ino == existing.Ino {
			return Result{}, errors.New("source and destination are the same item")
		}
		if isDir(before) && isDir(existing) {
			return e.merge(ctx, src, dst, move, policy)
		}
		if policy == Skip {
			return Result{Skipped: 1}, nil
		}
		if policy == Rename {
			original := dst.name
			extension := ""
			if !isDir(before) {
				extension = filepath.Ext(original)
			}
			stem := strings.TrimSuffix(original, extension)
			for n := 1; ; n++ {
				dst.name = fmt.Sprintf("%s (%d)%s", stem, n, extension)
				_, err = dst.stat()
				if errors.Is(err, unix.ENOENT) {
					break
				}
				if err != nil {
					return Result{}, err
				}
			}
			expected = ""
		} else {
			if !regular(existing) || !regular(before) {
				return Result{}, errors.New("file and folder types differ; rename or skip this item")
			}
			if policy != Replace && expected == "" {
				return Result{}, errors.New("destination already exists")
			}
			if expected != "" && expected != revision(existing) {
				return Result{}, errors.New("destination changed; choose what to do again")
			}
			expected = revision(existing)
		}
	} else if expected != "" {
		return Result{}, errors.New("destination changed; choose what to do again")
	}
	if move && expected == "" {
		e.capability(false)
		err = src.rename(dst, unix.RENAME_NOREPLACE)
		e.capability(true)
		if err == nil {
			if err = src.parent.Sync(); err != nil {
				return Result{}, err
			}
			return Result{Copied: 1}, dst.parent.Sync()
		}
		if !errors.Is(err, unix.EXDEV) {
			return Result{}, err
		}
	}
	holder, stage, err := staging(dst)
	if err != nil {
		return Result{}, err
	}
	defer holder.Close()
	preserve := false
	defer func() {
		if !preserve {
			_ = removeTree(context.Background(), target{dst.parent, filepath.Base(holder.Name())})
		}
	}()
	journal := map[string]any{"source": filepath.Join(src.parent.Name(), src.name), "destination": filepath.Join(dst.parent.Name(), dst.name), "move": move, "phase": "copying", "replace_revision": expected}
	save := func(phase string) error {
		journal["phase"] = phase
		raw, _ := json.Marshal(journal)
		f, err := target{holder, "transfer.json"}.open(unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(raw)
		if err == nil {
			err = f.Sync()
		}
		f.Close()
		if err == nil {
			err = holder.Sync()
		}
		return err
	}
	if err = save("copying"); err != nil {
		return Result{}, err
	}
	snapshot, err := e.snapshot(ctx, src)
	if err != nil {
		return Result{}, err
	}
	if err = e.copy(ctx, src, stage); err != nil {
		return Result{}, err
	}
	current, err := e.snapshot(ctx, src)
	if err != nil || !reflect.DeepEqual(snapshot, current) {
		return Result{}, errors.New("source changed; original data was preserved")
	}
	after, err := src.stat()
	if err != nil || !same(before, after) {
		return Result{}, errors.New("source changed; original data was preserved")
	}
	if err = e.check(ctx); err != nil {
		return Result{}, err
	}
	e.capability(false)
	defer e.capability(true)
	if err = save("publishing"); err != nil {
		return Result{}, err
	}
	uncertain, err := publish(stage, dst, expected)
	preserve = uncertain
	if err != nil {
		return Result{}, err
	}
	preserve = true
	if err = dst.parent.Sync(); err != nil {
		return Result{}, err
	}
	if err = save("published"); err != nil {
		return Result{}, err
	}
	if move {
		// Recursive removal is allowed only after a fresh content/metadata comparison.
		current, err := e.snapshot(ctx, src)
		if err != nil || !reflect.DeepEqual(snapshot, current) {
			return Result{}, errors.New("source changed; both copies preserved")
		}
		if err = equalTree(src, dst); err != nil {
			return Result{}, err
		}
		if err = removeTree(ctx, src); err != nil {
			return Result{}, err
		}
		if err = src.parent.Sync(); err != nil {
			return Result{}, err
		}
	}
	preserve = false
	return Result{Copied: 1}, nil
}
func staging(dst target) (*os.File, target, error) {
	for {
		var id [12]byte
		if _, e := rand.Read(id[:]); e != nil {
			return nil, target{}, e
		}
		name := ".panasms-copy-" + hex.EncodeToString(id[:])
		e := unix.Mkdirat(int(dst.parent.Fd()), name, 0700)
		if errors.Is(e, unix.EEXIST) {
			continue
		}
		if e != nil {
			return nil, target{}, e
		}
		f, e := (target{dst.parent, name}).open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if e != nil {
			return nil, target{}, e
		}
		return f, target{f, "content"}, nil
	}
}
func publish(stage, dst target, expected string) (bool, error) {
	if expected == "" {
		return false, stage.rename(dst, unix.RENAME_NOREPLACE)
	}
	s, err := dst.stat()
	if err != nil || !regular(s) || revision(s) != expected {
		return false, errors.New("destination changed; choose what to do again")
	}
	staged, err := stage.stat()
	if err != nil {
		return false, err
	}
	if err = stage.rename(dst, unix.RENAME_EXCHANGE); err != nil {
		return false, err
	}
	old, err := stage.stat()
	if err == nil && revision(old) == expected {
		return false, nil
	}
	now, err := dst.stat()
	if err == nil && revision(now) == revision(staged) {
		if err = stage.rename(dst, unix.RENAME_EXCHANGE); err == nil {
			return false, errors.New("destination changed; replacement rolled back")
		}
	}
	return true, errors.New("replacement could not be verified; previous data retained in transfer staging folder")
}
func (e *Engine) copy(ctx context.Context, src, dst target) error {
	if err := e.check(ctx); err != nil {
		return err
	}
	before, err := src.stat()
	if err != nil {
		return err
	}
	if isDir(before) {
		if err = unix.Mkdirat(int(dst.parent.Fd()), dst.name, 0777); err != nil {
			return err
		}
		sf, err := src.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		defer sf.Close()
		df, err := dst.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		defer df.Close()
		names, err := sf.Readdirnames(-1)
		if err != nil {
			return err
		}
		for _, name := range names {
			if err = e.copy(ctx, target{sf, name}, target{df, name}); err != nil {
				return err
			}
		}
		if err = metadata(sf, df, before); err != nil {
			return err
		}
		return df.Sync()
	}
	if before.Mode&unix.S_IFMT == unix.S_IFLNK {
		buf := make([]byte, 4096)
		n, err := unix.Readlinkat(int(src.parent.Fd()), src.name, buf)
		if err != nil {
			return err
		}
		return unix.Symlinkat(string(buf[:n]), int(dst.parent.Fd()), dst.name)
	}
	if !regular(before) {
		return errors.New("only regular files, folders and symbolic links can be copied")
	}
	sf, err := src.open(unix.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer sf.Close()
	var opened unix.Stat_t
	if err = unix.Fstat(int(sf.Fd()), &opened); err != nil || !same(before, opened) {
		return errors.New("source changed before copying")
	}
	df, err := dst.open(unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0666)
	if err != nil {
		return err
	}
	defer df.Close()
	buf := make([]byte, 1<<20)
	for {
		if err = e.check(ctx); err != nil {
			return err
		}
		n, readErr := sf.Read(buf)
		if n > 0 {
			if _, err = df.Write(buf[:n]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err = metadata(sf, df, before); err != nil {
		return err
	}
	if err = df.Sync(); err != nil {
		return err
	}
	if err = unix.Fstat(int(sf.Fd()), &opened); err != nil || !same(before, opened) {
		return errors.New("source changed during copying; original data was preserved")
	}
	after, err := src.stat()
	if err != nil || !same(before, after) {
		return errors.New("source changed during copying; original data was preserved")
	}
	return nil
}
func (e *Engine) merge(ctx context.Context, src, dst target, move bool, policy Conflict) (Result, error) {
	sf, err := src.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return Result{}, err
	}
	defer sf.Close()
	df, err := dst.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return Result{}, err
	}
	defer df.Close()
	names, err := sf.Readdirnames(-1)
	if err != nil {
		return Result{}, err
	}
	result := Result{}
	for _, name := range names {
		r, err := e.transfer(ctx, target{sf, name}, target{df, name}, move, policy, "")
		result.Copied += r.Copied
		result.Skipped += r.Skipped
		if err != nil {
			return result, err
		}
	}
	if move {
		err = unix.Unlinkat(int(src.parent.Fd()), src.name, unix.AT_REMOVEDIR)
		if err != nil && !errors.Is(err, unix.ENOTEMPTY) {
			return result, err
		}
		if err = src.parent.Sync(); err != nil {
			return result, err
		}
	}
	return result, df.Sync()
}
func removeTree(ctx context.Context, t target) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, err := t.stat()
	if err != nil {
		return err
	}
	if !isDir(s) {
		return unix.Unlinkat(int(t.parent.Fd()), t.name, 0)
	}
	f, err := t.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, n := range names {
		if err = removeTree(ctx, target{f, n}); err != nil {
			return err
		}
	}
	return unix.Unlinkat(int(t.parent.Fd()), t.name, unix.AT_REMOVEDIR)
}
func equalTree(a, b target) error {
	sa, err := a.stat()
	if err != nil {
		return err
	}
	sb, err := b.stat()
	if err != nil {
		return err
	}
	if isDir(sa) && isDir(sb) {
		af, err := a.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		defer af.Close()
		bf, err := b.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		defer bf.Close()
		an, err := af.Readdirnames(-1)
		if err != nil {
			return err
		}
		bn, err := bf.Readdirnames(-1)
		if err != nil {
			return err
		}
		if len(an) != len(bn) {
			return errors.New("source changed; both copies preserved")
		}
		for _, n := range an {
			if err = equalTree(target{af, n}, target{bf, n}); err != nil {
				return err
			}
		}
		return nil
	}
	if sa.Mode&unix.S_IFMT == unix.S_IFLNK && sb.Mode&unix.S_IFMT == unix.S_IFLNK {
		x, y := make([]byte, 4096), make([]byte, 4096)
		xn, e := unix.Readlinkat(int(a.parent.Fd()), a.name, x)
		if e != nil {
			return e
		}
		yn, e := unix.Readlinkat(int(b.parent.Fd()), b.name, y)
		if e != nil {
			return e
		}
		if string(x[:xn]) != string(y[:yn]) {
			return errors.New("source changed; both copies preserved")
		}
		return nil
	}
	if !regular(sa) || !regular(sb) || sa.Size != sb.Size {
		return errors.New("source changed; both copies preserved")
	}
	hash := func(t target) ([]byte, error) {
		f, e := t.open(unix.O_RDONLY, 0)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		h := sha256.New()
		_, e = io.Copy(h, f)
		return h.Sum(nil), e
	}
	ah, err := hash(a)
	if err != nil {
		return err
	}
	bh, err := hash(b)
	if err != nil {
		return err
	}
	if string(ah) != string(bh) {
		return errors.New("source changed; both copies preserved")
	}
	return nil
}

func (e *Engine) snapshot(ctx context.Context, t target) (map[string]string, error) {
	result := map[string]string{}
	var visit func(target, string) error
	visit = func(t target, p string) error {
		if err := e.check(ctx); err != nil {
			return err
		}
		s, err := t.stat()
		if err != nil {
			return err
		}
		result[p] = fmt.Sprintf("%s:%d:%d", revision(s), s.Ctim.Sec, s.Ctim.Nsec)
		if !isDir(s) {
			return nil
		}
		f, err := t.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		names, err := f.Readdirnames(-1)
		if err != nil {
			return err
		}
		for _, name := range names {
			if err = visit(target{f, name}, p+"/"+name); err != nil {
				return err
			}
		}
		return nil
	}
	err := visit(t, "")
	return result, err
}

func metadata(source, destination *os.File, s unix.Stat_t) error {
	if err := unix.Fchmod(int(destination.Fd()), s.Mode&0777); err != nil {
		return err
	}
	size, err := unix.Flistxattr(int(source.Fd()), nil)
	if err != nil && !errors.Is(err, unix.ENOTSUP) {
		return err
	}
	if size > 1<<20 {
		return errors.New("file metadata is too large")
	}
	if size > 0 {
		names := make([]byte, size)
		n, err := unix.Flistxattr(int(source.Fd()), names)
		if err != nil {
			return err
		}
		for _, name := range strings.Split(string(names[:n]), "\x00") {
			if !strings.HasPrefix(name, "user.") && name != "system.posix_acl_access" && name != "system.posix_acl_default" {
				continue
			}
			size, err := unix.Fgetxattr(int(source.Fd()), name, nil)
			if err != nil {
				return err
			}
			if size > 1<<20 {
				return errors.New("file metadata is too large")
			}
			value := make([]byte, size)
			n, err = unix.Fgetxattr(int(source.Fd()), name, value)
			if err != nil {
				return err
			}
			if err = unix.Fsetxattr(int(destination.Fd()), name, value[:n], 0); err != nil && !errors.Is(err, unix.ENOTSUP) {
				return err
			}
		}
	}
	return unix.UtimesNanoAt(int(destination.Fd()), "", []unix.Timespec{s.Atim, s.Mtim}, unix.AT_EMPTY_PATH)
}
