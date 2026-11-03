package archiveview

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PaNasMs/module-files/internal/cloudfs"
	"github.com/PaNasMs/module-sdk/external"
)

func TestLargeCloudZipOpensAsReadOnlyFolder(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "large.zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	member, err := z.CreateHeader(&zip.FileHeader{Name: "payload", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	zeros, err := os.Open("/dev/zero")
	if err != nil {
		t.Fatal(err)
	}
	defer zeros.Close()
	if _, err = io.CopyN(member, zeros, (64<<20)+1); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "fake-rclone")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\nlsjson) echo '[{\"Name\":\"large.zip\",\"Size\":%d}]';;\ncat) exec cat \"$(dirname \"$0\")/large.zip\";;\n*) exit 1;;\nesac\n", info.Size())
	if err = os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s := Store{Client: &cloudfs.Client{Binary: fake, Broker: func(context.Context, string) (external.Access, error) {
		return external.Access{Scope: "https://www.googleapis.com/auth/drive", TokenType: "Bearer", AccessToken: "fixture", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}}}
	defer s.Close()
	root := "arc:" + url.QueryEscape("cloud:"+strings.Repeat("a", 32)+"/large.zip")
	entries, err := s.List(context.Background(), root)
	if err != nil || len(entries) != 1 || entries[0].Name != "payload" || entries[0].Size != (64<<20)+1 {
		t.Fatalf("listing: %+v, %v", entries, err)
	}
	if !s.ReadOnly(context.Background(), root) {
		t.Fatal("large preview unexpectedly writable")
	}
}
