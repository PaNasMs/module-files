package localfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// TrashInfoName is the note written next to a trashed item, inside its container:
// <root>/.panasms-trash-<uid>/<nanoseconds>-<random>/{<item>, .panasms-trash-info.json}.
// Items trashed by earlier versions have no note and keep working without an original path.
const TrashInfoName = ".panasms-trash-info.json"
const trashInfoStaging = TrashInfoName + ".tmp"
const trashInfoLimit = 16384

var trashContainer = regexp.MustCompile(`^\d{19,}-[a-z0-9_]{8}$`)

type trashInfo struct {
	Version  int    `json:"version"`
	Original string `json:"original"`
}

func TrashName() string               { return fmt.Sprintf(".panasms-trash-%d", os.Geteuid()) }
func TrashContainer(name string) bool { return trashContainer.MatchString(name) }
func TrashMetadata(name string) bool  { return name == TrashInfoName || name == trashInfoStaging }

// TrashDeleted derives the deletion time, in seconds, from a container name.
func TrashDeleted(container string) float64 {
	digits, _, _ := strings.Cut(container, "-")
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || !TrashContainer(container) {
		return 0
	}
	return float64(n) / 1e9
}

func writeTrashInfo(holder *os.File, original string) error {
	raw, err := json.Marshal(trashInfo{Version: 1, Original: original})
	if err != nil {
		return err
	}
	stage, final := target{holder, trashInfoStaging}, target{holder, TrashInfoName}
	f, err := stage.open(unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err == nil {
		err = stage.rename(final, unix.RENAME_NOREPLACE)
	}
	if err != nil {
		_ = unix.Unlinkat(int(holder.Fd()), trashInfoStaging, 0)
		return err
	}
	return holder.Sync()
}

// TrashOriginal returns the path recorded for a trash container, or "" when there is
// no usable note. The note is untrusted: it must be a small regular file owned by the
// caller, opened without following links, holding a clean absolute path. The caller
// still has to check that the path belongs to the storage that holds this trash.
func TrashOriginal(container string) string {
	t, err := resolve(container)
	if err != nil {
		return ""
	}
	defer t.close()
	dir, err := t.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return ""
	}
	defer dir.Close()
	f, err := (target{dir, TrashInfoName}).open(unix.O_RDONLY, 0)
	if err != nil {
		return ""
	}
	defer f.Close()
	var s unix.Stat_t
	if unix.Fstat(int(f.Fd()), &s) != nil || !regular(s) || s.Uid != uint32(os.Geteuid()) || s.Size > trashInfoLimit {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(f, trashInfoLimit+1))
	if err != nil || len(raw) > trashInfoLimit {
		return ""
	}
	var info trashInfo
	if json.Unmarshal(raw, &info) != nil || info.Version != 1 {
		return ""
	}
	p := info.Original
	if len(p) > 4096 || !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" || strings.ContainsRune(p, 0) {
		return ""
	}
	return p
}

// TrashCleanup removes the container of a restored or deleted item once nothing but
// the note is left in it. Any other path, and any container still holding data, is
// left alone.
func TrashCleanup(item string) error {
	container := filepath.Dir(item)
	if !TrashContainer(filepath.Base(container)) || filepath.Base(filepath.Dir(container)) != TrashName() {
		return nil
	}
	t, err := resolve(container)
	if err != nil {
		return err
	}
	defer t.close()
	dir, err := t.open(unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer dir.Close()
	var s unix.Stat_t
	if err = unix.Fstat(int(dir.Fd()), &s); err != nil {
		return err
	}
	if s.Uid != uint32(os.Geteuid()) {
		return errors.New("invalid trash directory ownership or permissions")
	}
	names, err := dir.Readdirnames(3)
	if err != nil && err != io.EOF {
		return err
	}
	for _, name := range names {
		if !TrashMetadata(name) {
			return nil
		}
	}
	for _, name := range names {
		// Unlinking without AT_REMOVEDIR never removes a directory that borrowed the name.
		if err = unix.Unlinkat(int(dir.Fd()), name, 0); err != nil {
			return err
		}
	}
	if err = unix.Unlinkat(int(t.parent.Fd()), t.name, unix.AT_REMOVEDIR); err != nil {
		return err
	}
	return t.parent.Sync()
}
