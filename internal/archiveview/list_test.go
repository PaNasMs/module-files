package archiveview

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestZipDirectory(t *testing.T) {
	for _, count := range []int{0, 1, 3001} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "fixture")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			z := zip.NewWriter(f)
			for i := 0; i < count; i++ {
				if _, err := z.Create(fmt.Sprintf("внутри/%d.txt", i)); err != nil {
					t.Fatal(err)
				}
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			items, err := List(context.Background(), f, "BACKUP.ZIP")
			if count > 3000 {
				if !errors.Is(err, errLimit) {
					t.Fatalf("limit: %v", err)
				}
				return
			}
			if err != nil || len(items) != count {
				t.Fatalf("items %v error %v", items, err)
			}
			if count > 0 && (items[0].Name != "внутри/0.txt" || items[0].Size == nil || *items[0].Size != 0) {
				t.Fatal(items)
			}
		})
	}
}

func TestCompressedTar(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	g := gzip.NewWriter(f)
	w := tar.NewWriter(g)
	if err := w.WriteHeader(&tar.Header{Name: "empty/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	items, err := List(context.Background(), f, "backup.tgz")
	if err != nil || len(items) != 1 || !items[0].Directory || items[0].Name != "empty" {
		t.Fatalf("items %v error %v", items, err)
	}
}

func TestCorrupt(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("damaged archive"); err != nil {
		t.Fatal(err)
	}
	if _, err := List(context.Background(), f, "backup.zip"); !errors.Is(err, errUnreadable) {
		t.Fatal(err)
	}
}
