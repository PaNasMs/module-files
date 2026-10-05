package cloudfs

import (
	"errors"
	"io"
	"os"
)

// SpoolLimit bounds an export that has no size in advance (Google Docs, Sheets, Slides).
const SpoolLimit = 2 << 30

var errSpoolLimit = errors.New("cloud document is too large to export")

type limitedWriter struct {
	w    io.Writer
	left int64
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.left {
		return 0, errSpoolLimit
	}
	n, err := l.w.Write(p)
	l.left -= int64(n)
	return n, err
}

// Spool runs read into a private, already unlinked temporary file and returns it positioned at the
// start together with its size. Providers export native documents on the fly, so their length is
// only known once the export has finished; the HTTP layer needs it before the first byte.
func Spool(read func(io.Writer) error, limit int64) (*os.File, int64, error) {
	f, err := os.CreateTemp("", "panasms-export-")
	if err != nil {
		return nil, 0, err
	}
	_ = os.Remove(f.Name())
	if err = read(&limitedWriter{w: f, left: limit}); err == nil {
		var size int64
		if size, err = f.Seek(0, io.SeekCurrent); err == nil {
			if _, err = f.Seek(0, io.SeekStart); err == nil {
				return f, size, nil
			}
		}
	}
	f.Close()
	return nil, 0, err
}
