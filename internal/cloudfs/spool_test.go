package cloudfs

import (
	"context"
	"errors"
	"github.com/PaNasMs/module-files/internal/localfs"
	"io"
	"os"
	"strings"
	"testing"
)

func TestSpoolReportsSizeAndLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	f, size, err := Spool(func(w io.Writer) error { _, e := io.Copy(w, strings.NewReader("exported document")); return e }, 1024)
	if err != nil || size != 17 {
		t.Fatalf("size %d, error %v", size, err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	if string(data) != "exported document" {
		t.Fatalf("content %q", data)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("temporary file left behind: %v", left)
	}
	if _, _, err = Spool(func(w io.Writer) error { _, e := w.Write(make([]byte, 2048)); return e }, 1024); !errors.Is(err, errSpoolLimit) {
		t.Fatalf("limit not enforced: %v", err)
	}
	if _, _, err = Spool(func(io.Writer) error { return errors.New("export failed") }, 1024); err == nil {
		t.Fatal("read error swallowed")
	}
}

func TestArchiveSpoolSizeAndSpace(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	client := &Client{run: func(_ context.Context, _ string, _ []string, _ io.Reader, out io.Writer) error {
		_, err := io.WriteString(out, "abc")
		return err
	}}
	for _, size := range []int64{-1, 3, 2, 4, 1 << 60} {
		f, err := client.OpenArchive(context.Background(), "fixture", localfs.Entry{Size: size})
		switch size {
		case -1, 3:
			if err != nil {
				t.Fatal(err)
			}
			b, e := io.ReadAll(f)
			f.Close()
			if e != nil || string(b) != "abc" {
				t.Fatal(b, e)
			}
		case 2, 4:
			if !errors.Is(err, errPreviewChanged) {
				t.Fatal(size, err)
			}
		default:
			if !errors.Is(err, errPreviewSpace) {
				t.Fatal(size, err)
			}
		}
		if files, _ := os.ReadDir(dir); len(files) != 0 {
			t.Fatal(files)
		}
	}
}
