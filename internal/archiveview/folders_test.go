package archiveview

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"github.com/PaNasMs/module-files/internal/cloudfs"
	"github.com/PaNasMs/module-files/internal/localfs"
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

func TestFolderBatchPublishesOnlyAfterAllMembersSucceed(t *testing.T) {
	file, root := zipFolder(t, map[string]string{"folder/one": "one", "folder/two": "two"})
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s := &Store{}
	defer s.Close()
	_, err = s.Within(ctx, root+"/folder", root+"/moved", func() (any, error) {
		if err := s.Mkdir(ctx, root+"/moved"); err != nil {
			return nil, err
		}
		if err := s.Delete(ctx, root+"/folder/one", false); err != nil {
			return nil, err
		}
		current, e := os.ReadFile(file)
		if e != nil || !bytes.Equal(before, current) {
			t.Fatal("partial folder edit became visible", e)
		}
		return nil, context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = s.Stat(ctx, root+"/folder/one"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Stat(ctx, root+"/moved"); !os.IsNotExist(err) {
		t.Fatal("cancelled edit visible", err)
	}
}

func TestPrivateStagingRemovedAfterReadOnlyFolderCopy(t *testing.T) {
	_, root := zipFolder(t, map[string]string{"src/file": "hello"})
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	ctx := context.Background()
	s := &Store{}
	defer s.Close()
	_, err := s.Within(ctx, root+"/src", root+"/dst", func() (any, error) {
		if e := s.Mkdir(ctx, root+"/dst"); e != nil {
			return nil, e
		}
		if e := s.Upload(ctx, root+"/dst/file", bytes.NewBufferString("hello"), 5, ""); e != nil {
			return nil, e
		}
		a, _, e := s.archive(ctx, root)
		if e != nil {
			return nil, e
		}
		entry := a.entries["dst"]
		mode := 0555
		entry.Mode = &mode
		a.entries["dst"] = entry
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := os.ReadDir(temp)
	if err != nil || len(items) != 0 {
		t.Fatalf("staging left behind: %v %v", items, err)
	}
}

func TestCrossArchiveMoveKeepsExecutableMode(t *testing.T) {
	_, src := zipFolder(t, nil)
	_, dst := zipFolder(t, nil)
	ctx := context.Background()
	s := &Store{}
	defer s.Close()
	mode := 0751
	if err := s.PutMember(ctx, src+"/tool", bytes.NewBufferString("run"), localfs.Entry{Size: 3, Mode: &mode}, ""); err != nil {
		t.Fatal(err)
	}
	transfer := cloudfs.Transfer{Cloud: s, Local: &localfs.Engine{}}
	if _, err := transfer.Run(ctx, src+"/tool", dst+"/tool", true, localfs.Ask, ""); err != nil {
		t.Fatal(err)
	}
	member, err := s.Stat(ctx, dst+"/tool")
	if err != nil || member.Mode == nil || *member.Mode&0777 != mode {
		t.Fatalf("destination mode %v, err %v", member.Mode, err)
	}
	if _, err = s.Stat(ctx, src+"/tool"); !os.IsNotExist(err) {
		t.Fatalf("source not removed: %v", err)
	}
}
