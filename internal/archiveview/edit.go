package archiveview

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/PaNasMs/module-files/internal/cloudfs"
	"github.com/PaNasMs/module-files/internal/localfs"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type boundedWriter struct {
	out  io.Writer
	left int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.left {
		return 0, errLimit
	}
	n, e := w.out.Write(p)
	w.left -= int64(n)
	return n, e
}
func run(ctx context.Context, dir string, fd *os.File, input io.Reader, out io.Writer, argv ...string) error {
	ctx, stop := context.WithTimeout(ctx, 5*time.Minute)
	defer stop()
	limits := []string{"--as=805306368", "--cpu=180", "--fsize=1073741824", "--core=0", "--"}
	cmd := exec.CommandContext(ctx, "/usr/bin/prlimit", append(limits, argv...)...)
	cmd.Dir = dir
	cmd.Env = []string{"LANG=C.UTF-8", "PATH=/usr/bin:/bin", "HOME=" + dir}
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	if fd != nil {
		cmd.ExtraFiles = []*os.File{fd}
	}
	cmd.Stdin = input
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Archive operation failed; the original archive was preserved")
	}
	return nil
}
func (a *folderArchive) read(ctx context.Context, name string, out io.Writer) error {
	e, ok := a.entries[name]
	if !ok {
		return os.ErrNotExist
	}
	if e.Directory || e.Link || e.Size == nil || *e.Size < 0 || *e.Size > editLimit {
		return errors.New("This archive member cannot be read")
	}
	if e.Index == stagedIndex && a.pending != nil {
		f, err := os.Open(filepath.Join(a.pending.tree, name))
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(out, f)
		return err
	}
	dir, err := os.MkdirTemp("", "panasms-member-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ext := suffix(a.source)
	alias := filepath.Join(dir, "source"+ext)
	fd := a.file
	if a.tarPath != "" {
		alias = a.tarPath
		fd = nil
	} else if codec := tarCodec(ext); codec != "" {
		alias = filepath.Join(dir, "source.tar")
		info, e := fd.Stat()
		if e != nil {
			return e
		}
		if err = filter(ctx, dir, alias, io.NewSectionReader(fd, 0, info.Size()), codec, "-dc"); err != nil {
			return err
		}
		fd = nil
	} else if err = os.Symlink("/proc/self/fd/3", alias); err != nil {
		return err
	}
	args := []string{"/usr/bin/unar", "-q", "-o", "-", "-i", "-k", "skip", "-nr"}
	limit := &boundedWriter{out, *e.Size}
	if err = run(ctx, dir, fd, nil, limit, append(args, alias, strconv.Itoa(e.Index))...); err != nil {
		return err
	}
	if limit.left != 0 {
		return io.ErrUnexpectedEOF
	}
	return nil
}
func copyToFile(p string, in io.Reader, limit int64, mode os.FileMode) (int64, error) {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(&boundedWriter{f, limit}, in)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return n, err
}
func (s *Store) Upload(ctx context.Context, value string, in io.Reader, size int64, expected string) error {
	if !cloudfs.IsArchive(value) {
		return s.Client.Upload(ctx, value, in, size, expected)
	}
	return s.PutMember(ctx, value, in, localfs.Entry{Size: size}, expected)
}
func (s *Store) Mkdir(ctx context.Context, value string) error {
	if !cloudfs.IsArchive(value) {
		return s.Client.Mkdir(ctx, value)
	}
	return s.PutMember(ctx, value, nil, localfs.Entry{Directory: true}, "")
}
func (s *Store) Delete(ctx context.Context, value string, directory bool) error {
	if !cloudfs.IsArchive(value) {
		return s.Client.Delete(ctx, value, directory)
	}
	return s.edit(ctx, value, "delete", nil, localfs.Entry{}, "")
}
func (s *Store) PutMember(ctx context.Context, value string, in io.Reader, src localfs.Entry, expected string) error {
	op := "put"
	if src.Directory {
		op = "mkdir"
	}
	return s.edit(ctx, value, op, in, src, expected)
}
func (s *Store) edit(ctx context.Context, value, op string, in io.Reader, src localfs.Entry, expected string) error {
	if err := s.Writable(ctx, value); err != nil {
		return err
	}
	a, name, err := s.archive(ctx, value)
	if err != nil {
		return err
	}
	stage := func() error { return a.pending.stage(a, name, op, in, src, expected) }
	if a.pending != nil {
		return stage()
	}
	return s.batch(ctx, a, stage)
}
func (s *Store) commit(ctx context.Context, a *folderArchive, b *batch) error {
	removed, added, err := b.changes(a)
	if err != nil {
		return err
	}
	// Apply directory modes last and restore parent access first on return.
	sort.Sort(sort.Reverse(sort.StringSlice(added)))
	for _, name := range added {
		entry := a.entries[name]
		staged := filepath.Join(b.tree, name)
		if entry.Modified != 0 {
			stamp := time.Unix(int64(entry.Modified), 0)
			if err = os.Chtimes(staged, stamp, stamp); err != nil {
				return err
			}
		}
		if entry.Mode != nil {
			if entry.Directory {
				defer os.Chmod(staged, 0700)
			}
			if err = os.Chmod(staged, os.FileMode(*entry.Mode&0777)); err != nil {
				return err
			}
		}
	}
	if len(removed) == 0 && len(added) == 0 {
		return nil
	}
	dir, tree := b.dir, b.tree
	ext := suffix(a.source)
	kind := strings.TrimPrefix(ext, ".")
	archive := filepath.Join(dir, "work"+ext)
	codec := tarCodec(ext)
	if codec != "" {
		kind = "tar"
		archive = filepath.Join(dir, "work.tar")
	}
	if codec == "" {
		_, err = copyToFile(archive, io.NewSectionReader(a.file, 0, editLimit+1), editLimit, 0600)
	} else {
		err = os.Rename(a.tarPath, archive)
	}
	if err != nil {
		return err
	}
	command := func(action string, members []string) error {
		if len(members) == 0 {
			return nil
		}
		var args []string
		if kind == "tar" {
			verb := "--append"
			if action == "d" {
				verb = "--delete"
			}
			args = []string{"/usr/bin/tar", "--file", archive, "--no-recursion", "--no-wildcards", verb, "--"}
		} else {
			binary := "/usr/bin/7z"
			if _, e := os.Stat(binary); os.IsNotExist(e) {
				binary = "/usr/bin/7zz"
			}
			args = []string{binary, action, "-t" + kind, "-mmt=1", "-mx=3", "-spd", "-ssc", "-y", "--", archive}
		}
		return run(ctx, tree, nil, nil, io.Discard, append(args, members...)...)
	}
	if err = command("d", removed); err != nil {
		return err
	}
	if err = command("a", added); err != nil {
		return err
	}
	plain := archive
	if codec != "" {
		f, e := os.Open(archive)
		if e != nil {
			return e
		}
		defer f.Close()
		archive = filepath.Join(dir, "updated"+ext)
		if err = filter(ctx, dir, archive, f, codec, "-c"); err != nil {
			return err
		}
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	next := folderArchive{source: a.source, file: f}
	if err = next.index(ctx); err != nil {
		return err
	}
	if codec != "" {
		next.tarPath = plain
	}
	if len(next.entries) != len(a.entries) {
		return errors.New("Archive update could not be verified")
	}
	for key, row := range a.entries {
		other, ok := next.entries[key]
		if !ok || other.Directory != row.Directory || other.Link != row.Link || !row.Directory && !sameSize(other.Size, row.Size) {
			return errors.New("Archive update could not be verified")
		}
	}
	for name, wantHash := range b.hashes {
		h := sha256.New()
		if err = next.read(ctx, name, h); err != nil {
			return err
		}
		if fmt.Sprintf("%x", h.Sum(nil)) != wantHash {
			return errors.New("Archive update could not be verified")
		}
	}
	// Publish only after verification, using the revision of the opened archive.
	if _, err = f.Seek(0, 0); err != nil {
		return err
	}
	if cloudfs.IsRemote(a.source) {
		info, e := f.Stat()
		if e != nil {
			return e
		}
		if info.Size() > cloudLimit {
			return errLimit
		}
		if cloudfs.IsCloud(a.source) {
			err = s.Client.ReplaceArchive(ctx, a.source, a.revision, a.remoteVersion, f, info.Size())
		} else {
			err = s.Upload(ctx, a.source, f, info.Size(), a.revision)
		}
	} else {
		err = localfs.ReplaceArchive(ctx, a.source, a.revision, f)
	}
	if err != nil {
		if errors.Is(err, localfs.ErrArchivePublished) || errors.Is(err, cloudfs.ErrArchiveUnconfirmed) {
			a.file.Close()
			delete(s.opened, a.source)
		}
		return err
	}
	// Publication already succeeded. Invalidate the old snapshot even if a
	// concurrent replacement prevents us from confirming what is installed.
	a.file.Close()
	delete(s.opened, a.source)
	updated, err := s.load(ctx, a.source)
	if err != nil {
		return localfs.ErrArchivePublished
	}
	same, err := sameContents(f, updated.file)
	if err != nil || !same {
		updated.file.Close()
		return localfs.ErrArchivePublished
	}
	updated.baseline = a.baseline
	s.opened[a.source] = updated
	return nil
}

func sameSize(a, b *int64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func tarCodec(ext string) string {
	return map[string]string{".tar.gz": "gzip", ".tar.bz2": "bzip2", ".tar.xz": "xz"}[ext]
}
func filter(ctx context.Context, dir, dest string, in io.Reader, codec, flag string) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = run(ctx, dir, nil, in, &boundedWriter{f, editLimit}, "/usr/bin/"+codec, flag)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func sameContents(a, b *os.File) (bool, error) {
	digest := func(f *os.File) (string, error) {
		h := sha256.New()
		_, err := io.Copy(h, io.NewSectionReader(f, 0, editLimit+1))
		return string(h.Sum(nil)), err
	}
	first, err := digest(a)
	if err != nil {
		return false, err
	}
	second, err := digest(b)
	return first == second, err
}
