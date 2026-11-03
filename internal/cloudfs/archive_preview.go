package cloudfs

import (
	"context"
	"errors"
	"os"

	"github.com/PaNasMs/module-files/internal/localfs"
	"golang.org/x/sys/unix"
)

var errPreviewSpace = errors.New("Not enough temporary space on the NAS to preview this archive")
var errPreviewChanged = errors.New("Cloud archive changed while opening it; reopen the archive")

type previewWriter struct {
	file                         *os.File
	expected, written, allowance int64
	err                          error
}

func previewSpace(file *os.File) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return 0, err
	}
	// Leave headroom for other NAS services on the same temporary filesystem.
	return max(0, int64(stat.Bavail)*stat.Bsize-(64<<20)), nil
}
func (w *previewWriter) Write(p []byte) (int, error) {
	n := int64(len(p))
	if w.expected >= 0 && n > w.expected-w.written {
		w.err = errPreviewChanged
		return 0, w.err
	}
	if n > w.allowance {
		free, err := previewSpace(w.file)
		if err != nil {
			w.err = err
			return 0, err
		}
		if n > free {
			w.err = errPreviewSpace
			return 0, w.err
		}
		w.allowance = min(free, max(n, 4<<20))
	}
	size, err := w.file.Write(p)
	if errors.Is(err, unix.ENOSPC) || errors.Is(err, unix.EDQUOT) {
		err = errPreviewSpace
	}
	w.written += int64(size)
	w.allowance -= int64(size)
	w.err = err
	return size, err
}

func (c *Client) OpenArchive(ctx context.Context, path string, item localfs.Entry) (*os.File, error) {
	if item.Directory {
		return nil, errors.New("Archive directory is unreadable, encrypted or incomplete")
	}
	file, _, err := spoolFile(func(f *os.File) error {
		free, err := previewSpace(f)
		if err != nil {
			return err
		}
		if item.Size > free {
			return errPreviewSpace
		}
		w := &previewWriter{file: f, expected: item.Size, allowance: min(free, 4<<20)}
		err = c.Read(ctx, path, w)
		if w.err != nil {
			return w.err
		}
		if err != nil {
			return err
		}
		if item.Size >= 0 && w.written != item.Size {
			return errPreviewChanged
		}
		return nil
	})
	return file, err
}
