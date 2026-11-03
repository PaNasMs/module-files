package archiveview

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func zipFolder(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, data := range files {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write([]byte(data)); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(t.TempDir(), "sample.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return p, "arc:" + url.QueryEscape(p)
}
func TestFolderWritesKeepConcurrentChanges(t *testing.T) {
	p, root := zipFolder(t, map[string]string{"folder/one": "one"})
	ctx := context.Background()
	first, second := &Store{}, &Store{}
	defer first.Close()
	defer second.Close()
	if _, e := first.List(ctx, root); e != nil {
		t.Fatal(e)
	}
	if _, e := second.List(ctx, root); e != nil {
		t.Fatal(e)
	}
	if e := first.Mkdir(ctx, root+"/created"); e != nil {
		t.Fatal(e)
	}
	if e := second.Delete(ctx, root+"/folder/one", false); e == nil {
		t.Fatal("stale edit replaced concurrent update")
	}
	fresh := &Store{}
	defer fresh.Close()
	if _, e := fresh.Stat(ctx, root+"/created"); e != nil {
		t.Fatal(e)
	}
	var got bytes.Buffer
	if e := fresh.Read(ctx, root+"/folder/one", &got); e != nil || got.String() != "one" {
		t.Fatalf("read %q: %v", got.String(), e)
	}
	info, e := os.Stat(p)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode: %v %v", info, e)
	}
}
func TestRejectUnsafeOrAmbiguousMember(t *testing.T) {
	for _, files := range []map[string]string{{"../outside": "no"}, {"folder": "file", "folder/child": "no"}} {
		_, root := zipFolder(t, files)
		s := &Store{}
		_, err := s.List(context.Background(), root)
		s.Close()
		if !errors.Is(err, errPath) {
			t.Fatalf("wanted path error, got %v", err)
		}
	}
}
func TestRefuseTransferIntoOwnArchive(t *testing.T) {
	file, root := zipFolder(t, map[string]string{"member": "test"})
	s := &Store{}
	defer s.Close()
	if err := s.CheckOperation(context.Background(), "execute", "file.move", file, root+"/copy"); err == nil {
		t.Fatal("accepted moving archive into itself")
	}
}
