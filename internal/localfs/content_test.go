package localfs

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestUploadFailuresPreserveDestination(t *testing.T) {
	for _, body := range []string{"short", "too much data"} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			dest := root + "/file"
			put(t, dest, "original")
			rev, _ := Revision(dest)
			if err := (&Engine{}).Upload(context.Background(), dest, strings.NewReader(body), 8, rev); err == nil {
				t.Fatal("accepted wrong length")
			}
			content(t, dest, "original")
			items, _ := os.ReadDir(root)
			if len(items) != 1 {
				t.Fatal("staging leak")
			}
		})
	}
}
func TestUploadConcurrentPublishOneWinner(t *testing.T) {
	root := t.TempDir()
	dest := root + "/file"
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, text := range []string{"first", "other"} {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			results <- (&Engine{}).Upload(context.Background(), dest, strings.NewReader(s), 5, "")
		}(text)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("%d publishers succeeded", success)
	}
	data, err := os.ReadFile(dest)
	if err != nil || (string(data) != "first" && string(data) != "other") {
		t.Fatal("partial data")
	}
}
func TestUploadDoesNotFollowLink(t *testing.T) {
	root := t.TempDir()
	put(t, root+"/target", "keep")
	os.Symlink(root+"/target", root+"/link")
	if err := (&Engine{}).Upload(context.Background(), root+"/link", strings.NewReader("new"), 3, ""); err == nil {
		t.Fatal("replaced link")
	}
	content(t, root+"/target", "keep")
}
func TestCancelledCopyDiscardsStage(t *testing.T) {
	root := t.TempDir()
	put(t, root+"/source", strings.Repeat("data", 1<<20))
	checks := 0
	engine := &Engine{Check: func() error {
		checks++
		if checks >= 5 {
			return context.Canceled
		}
		return nil
	}}
	_, err := engine.Transfer(context.Background(), root+"/source", root+"/target", false, Ask, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	items, _ := os.ReadDir(root)
	if len(items) != 1 {
		t.Fatal("partial copy published or leaked")
	}
}
func TestNestedModificationPreventsPublication(t *testing.T) {
	root := t.TempDir()
	put(t, root+"/source/sub/file", "old")
	changed := false
	engine := &Engine{Check: func() error {
		files, _ := filepath.Glob(root + "/.panasms-copy-*/content/sub/file")
		if len(files) > 0 && !changed {
			put(t, root+"/source/sub/file", "new")
			changed = true
		}
		return nil
	}}
	if _, err := engine.Transfer(context.Background(), root+"/source", root+"/target", false, Ask, ""); err == nil {
		t.Fatal("published concurrent change")
	}
	if _, err := os.Stat(root + "/target"); !os.IsNotExist(err) {
		t.Fatal("published destination")
	}
}
func TestNestedLinksCopiedWithoutFollowing(t *testing.T) {
	root := t.TempDir()
	put(t, root+"/source/file", "data")
	os.Symlink("file", root+"/source/link")
	_, err := (&Engine{}).Transfer(context.Background(), root+"/source", root+"/dest", false, Ask, "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(root + "/dest/link")
	if err != nil || target != "file" {
		t.Fatal(target, err)
	}
}
func TestSpecialFilesRejectedWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	if err := unix.Mkfifo(root+"/fifo", 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root + "/fifo"); err == nil {
		t.Fatal("opened fifo")
	}
	_, err := (&Engine{}).Transfer(context.Background(), root+"/fifo", root+"/target", false, Ask, "")
	if err == nil {
		t.Fatal("copied fifo")
	}
}
func TestThumbnailAndOrientation(t *testing.T) {
	root := t.TempDir()
	file, err := os.Create(root + "/image.png")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 800, 400))
	img.Set(0, 0, color.White)
	if err = png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	file.Close()
	data, err := Thumbnail(root + "/image.png")
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || config.Width != 256 || config.Height != 128 {
		t.Fatal(config, format, err)
	}
	for _, input := range [][]byte{nil, {0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff}, []byte("not an image")} {
		if jpegOrientation(input) != 1 {
			t.Fatal("unsafe EXIF parsing")
		}
	}
}
func TestDownloadDetectsMutation(t *testing.T) {
	root := t.TempDir()
	put(t, root+"/file", "original")
	f, err := Open(root + "/file")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	writer := &changeWriter{change: func() { put(t, root+"/file", "modified") }}
	if err = StreamFile(f, writer); err == nil {
		t.Fatal("did not detect changed source")
	}
}

type changeWriter struct{ change func() }

func (w *changeWriter) Write(p []byte) (int, error) { w.change(); return len(p), nil }
func TestBoundedUpload(t *testing.T) {
	root := t.TempDir()
	reader := &boundedReader{remaining: 3 << 20}
	if err := (&Engine{}).Upload(context.Background(), root+"/file", reader, reader.remaining, ""); err != nil {
		t.Fatal(err)
	}
	if reader.largest > 1<<20 {
		t.Fatal(reader.largest)
	}
}

type boundedReader struct {
	remaining int64
	largest   int
}

func (r *boundedReader) Read(b []byte) (int, error) {
	r.largest = max(r.largest, len(b))
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(b), int(r.remaining))
	clear(b[:n])
	r.remaining -= int64(n)
	return n, nil
}

func TestTrashPreservesMaximumUTF8NamesAndDistinctCopies(t *testing.T) {
	root := t.TempDir()
	name := strings.Repeat("я", 125) + "a.pdf"
	target := filepath.Join(root, name)
	engine := &Engine{}
	var saved []string
	for _, value := range []string{"first", "second"} {
		put(t, target, value)
		result, err := Trash(context.Background(), target, root, engine)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(result) != name {
			t.Fatal(result)
		}
		content(t, result, value)
		saved = append(saved, result)
	}
	if saved[0] == saved[1] {
		t.Fatal("trash copies collided")
	}
	if _, err := engine.Transfer(context.Background(), saved[0], target, true, Ask, ""); err != nil {
		t.Fatal(err)
	}
	content(t, target, "first")
	content(t, saved[1], "second")
}
func TestDownloadBinaryEmptyAndSymlink(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	for _, value := range []string{"", strings.Repeat("\x00\xff\nOK\n", 10000)} {
		put(t, file, value)
		var out bytes.Buffer
		if err := Download(file, &out); err != nil {
			t.Fatal(err)
		}
		if out.String() != value {
			t.Fatal("download changed bytes")
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if err := Download(link, io.Discard); err == nil {
		t.Fatal("download followed symlink")
	}
}

func TestCancelledUploadDiscardsStage(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	chunks := 0
	reader := readerFunc(func(p []byte) (int, error) {
		chunks++
		if chunks == 3 {
			cancel()
		}
		for i := range p {
			p[i] = 'x'
		}
		return len(p), nil
	})
	err := (&Engine{}).Upload(ctx, root+"/target", reader, 8<<20, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	items, _ := os.ReadDir(root)
	if len(items) != 0 {
		t.Fatal("cancelled upload left files behind:", items)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestCancelAfterLastChunkDoesNotPublish(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	data := strings.Repeat("y", 4096)
	reader := readerFunc(func(p []byte) (int, error) {
		if len(data) == 0 {
			return 0, io.EOF
		}
		n := copy(p, data)
		data = data[n:]
		if len(data) == 0 {
			cancel()
		}
		return n, nil
	})
	err := (&Engine{}).Upload(ctx, root+"/target", reader, 4096, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	items, _ := os.ReadDir(root)
	if len(items) != 0 {
		t.Fatal("cancelled upload published or leaked:", items)
	}
}
