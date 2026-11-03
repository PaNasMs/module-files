package archiveview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var errUnreadable = errors.New("Archive directory is unreadable, encrypted or incomplete")
var errLimit = errors.New("Archive exceeds the preview limits")

type Entry struct {
	Mode      *int    `json:"-"`
	Name      string  `json:"name"`
	Index     int     `json:"-"`
	Link      bool    `json:"-"`
	Encrypted bool    `json:"-"`
	Modified  float64 `json:"-"`
	Directory bool    `json:"directory,omitempty"`
	Size      *int64  `json:"size,omitempty"`
}

func suffix(name string) string {
	name = strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tgz", ".tbz2", ".txz", ".tar", ".zip", ".rar", ".7z"} {
		if strings.HasSuffix(name, ext) {
			switch ext {
			case ".tgz":
				return ".tar.gz"
			case ".tbz2":
				return ".tar.bz2"
			case ".txz":
				return ".tar.xz"
			}
			return ext
		}
	}
	return ""
}

func List(ctx context.Context, file *os.File, name string) ([]Entry, error) {
	ext := suffix(name)
	if ext == "" {
		return nil, errUnreadable
	}
	// lsar needs a compound suffix to see through compressed TAR wrappers. Point
	// only at the descriptor already opened under the user's no-follow policy.
	dir, err := os.MkdirTemp("", "panasms-archive-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "archive"+ext)
	if err := os.Symlink("/proc/self/fd/3", path); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"--as=402653184", "--cpu=10", "--core=0", "--fsize=0", "--", "/usr/bin/lsar", "-j"}
	// Recent lsar versions can omit repeated solid RAR metadata. Older
	// distributions reject that flag, even for ordinary ZIP archives.
	if ext == ".rar" {
		help, err := exec.CommandContext(ctx, "/usr/bin/lsar", "-h").Output()
		if err == nil && bytes.Contains(help, []byte("(-jss)")) {
			args = append(args, "-jss")
		}
	}
	if !strings.HasPrefix(ext, ".tar.") {
		args = append(args, "-nr")
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/prlimit", append(args, path)...)
	cmd.ExtraFiles = []*os.File{file}
	cmd.Env = []string{"LANG=C.UTF-8", "HOME=" + dir, "PATH=/usr/bin:/bin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	const maxJSON = 6 << 20
	raw, err := io.ReadAll(io.LimitReader(out, maxJSON+1))
	if err != nil || len(raw) > maxJSON {
		cancel()
	}
	exitErr := cmd.Wait()
	if len(raw) > maxJSON || ctx.Err() == context.DeadlineExceeded {
		return nil, errLimit
	}
	if err != nil || exitErr != nil {
		return nil, errUnreadable
	}
	var result struct {
		Format   string `json:"lsarFormatName"`
		Inner    string `json:"lsarInnerFormatName"`
		Error    *int   `json:"lsarError"`
		Contents []struct {
			Index     int    `json:"XADIndex"`
			Link      int    `json:"XADIsLink"`
			Hardlink  int    `json:"XADIsHardLink"`
			Encrypted int    `json:"XADIsEncrypted"`
			Date      string `json:"XADLastModificationDate"`
			Mode      *int   `json:"XADPosixPermissions"`
			Name      string `json:"XADFileName"`
			Directory int    `json:"XADIsDirectory"`
			Size      *int64 `json:"XADFileSize"`
			Corrupt   int    `json:"XADIsCorrupted"`
		} `json:"lsarContents"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Error != nil || result.Format == "" || result.Contents == nil || strings.HasPrefix(ext, ".tar.") && !strings.EqualFold(result.Inner, "Tar") {
		return nil, errUnreadable
	}
	if len(result.Contents) > 3000 {
		return nil, errLimit
	}
	entries := make([]Entry, 0, len(result.Contents))
	nameBytes := 0
	for _, row := range result.Contents {
		if row.Corrupt != 0 || row.Name == "" {
			return nil, errUnreadable
		}
		nameBytes += len(row.Name)
		if nameBytes > 256<<10 {
			return nil, errLimit
		}
		date, _ := time.Parse("2006-01-02 15:04:05 -0700", row.Date)
		var modified float64
		if !date.IsZero() {
			modified = float64(date.Unix())
		}
		kind := 0
		if row.Mode != nil {
			kind = *row.Mode & syscall.S_IFMT
		}
		entries = append(entries, Entry{Mode: row.Mode, Name: row.Name, Directory: row.Directory != 0, Size: row.Size, Index: row.Index, Link: row.Link != 0 || row.Hardlink != 0 || kind != 0 && kind != syscall.S_IFREG && kind != syscall.S_IFDIR, Modified: modified, Encrypted: row.Encrypted != 0})
	}
	return entries, nil
}
