package operations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PaNasMs/module-files/internal/localfs"
)

// A temporary directory stands in for one storage root.
func storage(t *testing.T) (string, []string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{root, "/"}
	old := places
	places = func(context.Context, string) (Listing, error) { return Listing{Roots: roots}, nil }
	t.Cleanup(func() { places = old })
	return root, roots
}
func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
func trash(t *testing.T, path string) string {
	t.Helper()
	value, err := Execute(context.Background(), "", Request{Action: "file.trash", Params: Params{Target: path}}, &localfs.Engine{})
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]string)["path"]
}
func note(t *testing.T, item, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(item), localfs.TrashInfoName), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func missing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s still exists: %v", path, err)
	}
}

func TestTrashRecordsOriginalLocation(t *testing.T) {
	root, roots := storage(t)
	source := filepath.Join(root, "docs", "report.txt")
	write(t, source, "data")
	item := trash(t, source)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(item), localfs.TrashInfoName))
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	if json.Unmarshal(raw, &info) != nil || info["version"] != float64(1) || info["original"] != source || len(info) != 2 {
		t.Fatalf("note: %s", raw)
	}
	if s, _ := os.Stat(filepath.Join(filepath.Dir(item), localfs.TrashInfoName)); s.Mode().Perm() != 0600 {
		t.Fatalf("note mode %v", s.Mode())
	}
	entries := containerEntries(filepath.Dir(item), roots)
	if len(entries) != 1 || entries[0].Name != "report.txt" || entries[0].Original != source || entries[0].Deleted < 1e9 {
		t.Fatalf("listing: %+v", entries)
	}
}

func TestTrashedFolderContentsKeepRelativeOrigin(t *testing.T) {
	root, roots := storage(t)
	write(t, filepath.Join(root, "album", "2024", "a.jpg"), "x")
	item := trash(t, filepath.Join(root, "album"))
	original, deleted := trashOrigin(filepath.Join(item, "2024", "a.jpg"), roots)
	if original != filepath.Join(root, "album", "2024", "a.jpg") || deleted == 0 {
		t.Fatal(original, deleted)
	}
}

func TestRestoreWithoutDestinationReturnsToOrigin(t *testing.T) {
	root, _ := storage(t)
	source := filepath.Join(root, "docs", "report.txt")
	write(t, source, "data")
	item := trash(t, source)
	request := Request{Action: "file.restore", Params: Params{Target: item}}
	plan, err := Plan(context.Background(), "", request)
	if err != nil || plan["details"].([]string)[1] != source {
		t.Fatal(plan, err)
	}
	if _, err = Execute(context.Background(), "", request, &localfs.Engine{}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(source); string(raw) != "data" {
		t.Fatalf("restored content %q", raw)
	}
	missing(t, filepath.Dir(item))
}

func TestRestoreToOriginReportsConflictAndMissingFolder(t *testing.T) {
	root, _ := storage(t)
	source := filepath.Join(root, "docs", "report.txt")
	write(t, source, "old")
	item := trash(t, source)
	write(t, source, "new")
	request := Request{Action: "file.restore", Params: Params{Target: item}}
	if _, err := Execute(context.Background(), "", request, &localfs.Engine{}); err == nil || err.Error() != "destination already exists" {
		t.Fatalf("taken name: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(context.Background(), "", request, &localfs.Engine{}); err == nil || err.Error() != "no such file or directory" {
		t.Fatalf("missing folder: %v", err)
	}
	if raw, _ := os.ReadFile(item); string(raw) != "old" {
		t.Fatal("trashed item was lost")
	}
	request.Params.Destination = filepath.Join(root, "report.txt")
	if _, err := Execute(context.Background(), "", request, &localfs.Engine{}); err != nil {
		t.Fatal(err)
	}
	missing(t, filepath.Dir(item))
}

func TestLegacyItemWithoutNote(t *testing.T) {
	root, roots := storage(t)
	source := filepath.Join(root, "old.txt")
	write(t, source, "data")
	item := trash(t, source)
	if err := os.Remove(filepath.Join(filepath.Dir(item), localfs.TrashInfoName)); err != nil {
		t.Fatal(err)
	}
	entries := containerEntries(filepath.Dir(item), roots)
	if len(entries) != 1 || entries[0].Original != "" || entries[0].Deleted == 0 {
		t.Fatalf("listing: %+v", entries)
	}
	request := Request{Action: "file.restore", Params: Params{Target: item}}
	if _, err := Plan(context.Background(), "", request); err == nil || !strings.HasPrefix(err.Error(), "original location is unknown") {
		t.Fatalf("plan without destination: %v", err)
	}
	request.Params.Destination = filepath.Join(root, "back.txt")
	if _, err := Execute(context.Background(), "", request, &localfs.Engine{}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(request.Params.Destination); string(raw) != "data" {
		t.Fatal("legacy restore failed")
	}
	missing(t, filepath.Dir(item))
}

func TestHostileNoteIsIgnored(t *testing.T) {
	root, roots := storage(t)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "docs", "report.txt")
	write(t, source, "data")
	item := trash(t, source)
	trashDir := filepath.Dir(filepath.Dir(item))
	quote := func(path string) string {
		raw, _ := json.Marshal(map[string]any{"version": 1, "original": path})
		return string(raw)
	}
	for name, text := range map[string]string{
		"not json":        "{",
		"wrong version":   `{"version":2,"original":"` + source + `"}`,
		"relative":        quote("docs/report.txt"),
		"unclean":         quote(root + "/docs/../docs/report.txt"),
		"trailing slash":  quote(source + "/"),
		"nul":             quote(source + "\x00"),
		"filesystem root": quote("/"),
		"other name":      quote(filepath.Join(root, "docs", "other.txt")),
		"other storage":   quote(filepath.Join(outside, "report.txt")),
		"system path":     quote("/etc/report.txt"),
		"inside trash":    quote(filepath.Join(trashDir, "x", "report.txt")),
		"oversized":       quote(source) + strings.Repeat(" ", 20000),
	} {
		note(t, item, text)
		if original, _ := trashOrigin(item, roots); original != "" {
			t.Fatalf("%s: accepted %q", name, original)
		}
	}
	// A note replaced by a symbolic link or a directory is never followed.
	info := filepath.Join(filepath.Dir(item), localfs.TrashInfoName)
	good := filepath.Join(root, "good.json")
	write(t, good, quote(source))
	if err = os.Remove(info); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(good, info); err != nil {
		t.Fatal(err)
	}
	if original, _ := trashOrigin(item, roots); original != "" {
		t.Fatalf("followed a symbolic link to %q", original)
	}
	if err = os.Remove(info); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(info, 0700); err != nil {
		t.Fatal(err)
	}
	if original, _ := trashOrigin(item, roots); original != "" {
		t.Fatal("accepted a directory")
	}
	// The listing still shows the item, and only the item.
	entries := containerEntries(filepath.Dir(item), roots)
	if len(entries) != 1 || entries[0].Name != "report.txt" || entries[0].Original != "" {
		t.Fatalf("listing: %+v", entries)
	}
	// A trash that is not on a known storage root is not trusted either.
	if err = os.Remove(info); err != nil {
		t.Fatal(err)
	}
	note(t, item, quote(source))
	if original, _ := trashOrigin(item, []string{"/"}); original != "" {
		t.Fatalf("accepted a note for an unknown root: %q", original)
	}
	if original, _ := trashOrigin(item, roots); original != source {
		t.Fatalf("valid note rejected: %q", original)
	}
}

func TestEmptyContainerIsRemoved(t *testing.T) {
	root, _ := storage(t)
	first, second := filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")
	write(t, first, "a")
	write(t, second, "b")
	a, b := trash(t, first), trash(t, second)
	if _, err := Execute(context.Background(), "", Request{Action: "file.delete", Params: Params{Target: a}}, &localfs.Engine{}); err != nil {
		t.Fatal(err)
	}
	missing(t, filepath.Dir(a))
	if raw, _ := os.ReadFile(b); string(raw) != "b" {
		t.Fatal("unrelated trashed item was touched")
	}
	// Extra content keeps the container and its note.
	write(t, filepath.Join(filepath.Dir(b), "stray"), "keep")
	if _, err := Execute(context.Background(), "", Request{Action: "file.delete", Params: Params{Target: b}}, &localfs.Engine{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"stray", localfs.TrashInfoName} {
		if _, err := os.Lstat(filepath.Join(filepath.Dir(b), name)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Folders that merely look similar are never swept.
	plain := filepath.Join(root, "1759600000000000000-0a1b2c3d", "file")
	write(t, plain, "x")
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
	if err := localfs.TrashCleanup(plain); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(plain)); err != nil {
		t.Fatal("removed a folder outside the trash")
	}
	// Deleting something deeper inside a trashed folder leaves the folder in the trash.
	write(t, filepath.Join(root, "album", "a.jpg"), "x")
	album := trash(t, filepath.Join(root, "album"))
	if _, err := Execute(context.Background(), "", Request{Action: "file.delete", Params: Params{Target: filepath.Join(album, "a.jpg")}}, &localfs.Engine{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(album); err != nil {
		t.Fatal("trashed folder disappeared")
	}
}

func TestItemNamedLikeTheNote(t *testing.T) {
	root, roots := storage(t)
	source := filepath.Join(root, localfs.TrashInfoName)
	write(t, source, "user data")
	item := trash(t, source)
	if raw, _ := os.ReadFile(item); string(raw) != "user data" {
		t.Fatalf("item overwritten: %q", raw)
	}
	entries := containerEntries(filepath.Dir(item), roots)
	if len(entries) != 1 || entries[0].Name != localfs.TrashInfoName || entries[0].Original != "" {
		t.Fatalf("listing: %+v", entries)
	}
	request := Request{Action: "file.restore", Params: Params{Target: item, Destination: source}}
	if _, err := Execute(context.Background(), "", request, &localfs.Engine{}); err != nil {
		t.Fatal(err)
	}
	missing(t, filepath.Dir(item))
}
