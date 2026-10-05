package cloudfs

import (
	"errors"
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
