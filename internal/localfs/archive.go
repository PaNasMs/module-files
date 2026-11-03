package localfs

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

// Keep the archive's access controls when atomically exchanging its contents.
func ReplaceArchive(ctx context.Context, path, expected string, data *os.File) error {
	dst, err := resolve(path)
	if err != nil {
		return err
	}
	defer dst.close()
	original, err := dst.open(unix.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer original.Close()
	if err = unix.Flock(int(original.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return err
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(original.Fd()), &st); err != nil {
		return err
	}
	if !regular(st) || revision(st) != expected || st.Nlink != 1 || st.Uid != uint32(os.Geteuid()) || st.Mode&07000 != 0 {
		return errors.New("Archive changed or cannot be safely replaced")
	}
	dir, stage, err := staging(dst)
	if err != nil {
		return err
	}
	defer dir.Close()
	preserve := false
	defer func() {
		if !preserve {
			_ = removeTree(context.Background(), target{dst.parent, filepath.Base(dir.Name())})
		}
	}()
	f, err := stage.open(unix.O_RDWR|unix.O_CREAT|unix.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = data.Seek(0, 0); err != nil {
		return err
	}
	if _, err = f.ReadFrom(data); err != nil {
		return err
	}
	if err = f.Chown(-1, int(st.Gid)); err != nil {
		return err
	}
	// The staging directory may inherit a default ACL from the archive's
	// parent. Restore only the original file's access ACL.
	if err = unix.Fremovexattr(int(f.Fd()), "system.posix_acl_access"); err != nil && !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.ENOTSUP) {
		return err
	}
	if err = metadata(original, f, st); err != nil {
		return err
	}
	if err = unix.UtimesNanoAt(int(f.Fd()), "", []unix.Timespec{st.Atim, {Nsec: unix.UTIME_NOW}}, unix.AT_EMPTY_PATH); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	preserve, err = publish(stage, dst, expected)
	if err != nil {
		return err
	}
	return dst.parent.Sync()
}
