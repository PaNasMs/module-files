package cloudfs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/PaNasMs/module-files/internal/localfs"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type Transfer struct {
	Cloud            *Client
	Local            *localfs.Engine
	Progress         func(float64)
	total, completed int64
	weights          map[string]int64
	lastProgress     time.Time
}

func (t *Transfer) List(ctx context.Context, folder string) ([]localfs.Entry, error) {
	if IsCloud(folder) {
		return t.Cloud.List(ctx, folder)
	}
	entries, _, err := localfs.List(folder)
	return entries, err
}
func (t *Transfer) Stat(ctx context.Context, value string) (localfs.Entry, error) {
	if IsCloud(value) {
		return t.Cloud.Stat(ctx, value)
	}
	entries, _, err := localfs.List(filepath.Dir(value))
	if err != nil {
		return localfs.Entry{}, err
	}
	for _, e := range entries {
		if e.Path == value {
			return e, nil
		}
	}
	return localfs.Entry{}, os.ErrNotExist
}
func (t *Transfer) read(ctx context.Context, value string, out io.Writer) error {
	if IsCloud(value) {
		return t.Cloud.Read(ctx, value, out)
	}
	return localfs.Download(value, out)
}
func (t *Transfer) mkdir(ctx context.Context, value string) error {
	if IsCloud(value) {
		return t.Cloud.Mkdir(ctx, value)
	}
	return localfs.Mkdir(value)
}
func (t *Transfer) remove(ctx context.Context, value string, directory bool) error {
	if IsCloud(value) {
		return t.Cloud.Delete(ctx, value, directory)
	}
	if directory {
		return os.Remove(value)
	}
	return localfs.Delete(ctx, value)
}
func (t *Transfer) copyStream(ctx context.Context, source, destination string, size int64, revision string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	hash := sha256.New()
	done := make(chan error, 1)
	go func() {
		err := t.read(ctx, source, io.MultiWriter(writer, hash, progressWriter{t}))
		writer.CloseWithError(err)
		done <- err
	}()
	var err error
	if IsCloud(destination) {
		err = t.Cloud.Write(ctx, destination, reader, size)
	} else {
		err = t.Local.Upload(ctx, destination, reader, size, revision)
	}
	reader.CloseWithError(err)
	if err != nil {
		cancel()
	}
	readErr := <-done
	if err != nil {
		return "", err
	}
	if readErr != nil {
		return "", readErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func (t *Transfer) Run(ctx context.Context, source, destination string, move bool, policy localfs.Conflict, expected string) (localfs.Result, error) {
	if t.Progress != nil {
		t.weights = map[string]int64{}
		total, err := t.measure(ctx, source)
		if err != nil {
			return localfs.Result{}, err
		}
		t.total, t.completed = total, 0
		t.Progress(0)
	}
	return t.runTransfer(ctx, source, destination, move, policy, expected)
}
func (t *Transfer) runTransfer(ctx context.Context, source, destination string, move bool, policy localfs.Conflict, expected string) (localfs.Result, error) {
	if !IsCloud(source) && !IsCloud(destination) {
		return t.Local.Transfer(ctx, source, destination, move, policy, expected)
	}
	if IsCloud(source) && move {
		_, rel, err := Parse(source)
		if err != nil || rel == "" {
			return localfs.Result{}, errors.New("cannot move an account root")
		}
	}
	if source == destination || strings.HasPrefix(destination, source+"/") || strings.HasPrefix(source, destination+"/") {
		return localfs.Result{}, errors.New("source and destination must not contain each other")
	}
	if err := ctx.Err(); err != nil {
		return localfs.Result{}, err
	}
	if t.Local.Check != nil {
		if err := t.Local.Check(); err != nil {
			return localfs.Result{}, err
		}
	}
	src, err := t.Stat(ctx, source)
	if err != nil {
		return localfs.Result{}, err
	}
	if src.Link {
		return localfs.Result{}, errors.New("symbolic links cannot be copied to cloud storage")
	}
	entries, err := t.List(ctx, folder(destination))
	if err != nil {
		return localfs.Result{}, err
	}
	var existing *localfs.Entry
	for i := range entries {
		if entries[i].Name == path.Base(destination) || (IsCloud(destination) && strings.EqualFold(entries[i].Name, path.Base(destination))) {
			existing = &entries[i]
			break
		}
	}
	if existing != nil && src.Directory && existing.Directory {
		destination = existing.Path
		return t.merge(ctx, source, destination, move, policy)
	}
	if existing != nil {
		if policy == localfs.Skip {
			t.advance(t.weights[source])
			return localfs.Result{Skipped: 1}, nil
		}
		if policy == localfs.Rename {
			stem, ext := path.Base(destination), ""
			if !src.Directory {
				ext = path.Ext(stem)
				stem = strings.TrimSuffix(stem, ext)
			}
			names := map[string]bool{}
			for _, e := range entries {
				names[strings.ToLower(e.Name)] = true
			}
			for n := 1; ; n++ {
				name := fmt.Sprintf("%s (%d)%s", stem, n, ext)
				if !names[strings.ToLower(name)] {
					destination = Join(folder(destination), name)
					break
				}
			}
			existing = nil
			expected = ""
		} else {
			if existing.Link || existing.Directory != src.Directory {
				return localfs.Result{}, errors.New("file and folder types differ; rename or skip this item")
			}
			if policy != localfs.Replace && expected == "" {
				return localfs.Result{}, errors.New("destination already exists")
			}
			if expected != "" && expected != existing.Revision {
				return localfs.Result{}, errors.New("destination changed; choose what to do again")
			}
			expected = existing.Revision
			destination = existing.Path
		}
	} else if expected != "" {
		return localfs.Result{}, errors.New("destination changed; choose what to do again")
	}
	if src.Directory {
		if err = t.mkdir(ctx, destination); err != nil {
			return localfs.Result{}, err
		}
		return t.merge(ctx, source, destination, move, policy)
	}
	actual := destination
	stage := ""
	if IsCloud(destination) {
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return localfs.Result{}, err
		}
		stage = Join(folder(destination), ".panasms-transfer-"+hex.EncodeToString(random[:]))
		actual = stage
	}
	if stage != "" {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 30e9)
			defer cancel()
			_ = t.Cloud.Delete(cleanup, stage, false)
		}()
	}
	hash, err := t.copyStream(ctx, source, actual, src.Size, expected)
	if err != nil {
		return localfs.Result{}, err
	}
	verification := sha256.New()
	if err = t.read(ctx, actual, io.MultiWriter(verification, progressWriter{t})); err != nil {
		return localfs.Result{}, err
	}
	if hex.EncodeToString(verification.Sum(nil)) != hash {
		return localfs.Result{}, errors.New("copy verification failed; original file preserved")
	}
	current, err := t.Stat(ctx, source)
	if err != nil || current.Revision != src.Revision {
		return localfs.Result{}, errors.New("source changed; original file preserved")
	}
	if stage != "" {
		dest, err := t.Stat(ctx, destination)
		if expected == "" {
			if !os.IsNotExist(err) {
				return localfs.Result{}, errors.New("destination changed; original file preserved")
			}
		} else if err != nil || dest.Revision != expected {
			return localfs.Result{}, errors.New("destination changed; original file preserved")
		}
		if err = t.Cloud.Publish(ctx, stage, destination); err != nil {
			return localfs.Result{}, err
		}
	}
	if move {
		current, err = t.Stat(ctx, source)
		if err != nil || current.Revision != src.Revision {
			return localfs.Result{}, errors.New("source changed; both copies preserved")
		}
		if err = t.remove(ctx, source, false); err != nil {
			return localfs.Result{}, err
		}
	}
	t.advance(1)
	return localfs.Result{Copied: 1}, nil
}
func folder(value string) string {
	if IsCloud(value) {
		return Parent(value)
	}
	return filepath.Dir(value)
}
func (t *Transfer) merge(ctx context.Context, source, destination string, move bool, policy localfs.Conflict) (localfs.Result, error) {
	entries, err := t.List(ctx, source)
	if err != nil {
		return localfs.Result{}, err
	}
	result := localfs.Result{}
	for _, entry := range entries {
		r, err := t.runTransfer(ctx, entry.Path, Join(destination, entry.Name), move, policy, "")
		result.Copied += r.Copied
		result.Skipped += r.Skipped
		if err != nil {
			return result, err
		}
	}
	if move {
		remaining, err := t.List(ctx, source)
		if err != nil {
			return result, err
		}
		if len(remaining) == 0 {
			if err = t.remove(ctx, source, true); err != nil {
				return result, err
			}
		}
	}
	t.advance(1)
	return result, nil
}

// Include verification reads and one completion unit per item, including empty files.
func (t *Transfer) measure(ctx context.Context, source string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	item, err := t.Stat(ctx, source)
	if err != nil {
		return 0, err
	}
	return t.measureEntry(ctx, item)
}
func (t *Transfer) measureEntry(ctx context.Context, item localfs.Entry) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	source := item.Path
	if item.Link {
		return 0, errors.New("symbolic links cannot be copied to cloud storage")
	}
	weight := int64(1)
	if item.Directory {
		entries, err := t.List(ctx, source)
		if err != nil {
			return 0, err
		}
		for _, entry := range entries {
			n, err := t.measureEntry(ctx, entry)
			if err != nil {
				return 0, err
			}
			weight += n
		}
	} else {
		if item.Size < 0 {
			return 0, errors.New("cloud document must be exported before copying")
		}
		weight += 2 * item.Size
	}
	t.weights[source] = weight
	return weight, nil
}
func (t *Transfer) advance(n int64) {
	if t.Progress == nil || t.total <= 0 {
		return
	}
	t.completed += n
	if time.Since(t.lastProgress) < time.Second && t.completed < t.total {
		return
	}
	t.lastProgress = time.Now()
	percent := min(99.0, 100*float64(t.completed)/float64(t.total))
	t.Progress(percent)
}

type progressWriter struct{ transfer *Transfer }

func (w progressWriter) Write(p []byte) (int, error) {
	w.transfer.advance(int64(len(p)))
	return len(p), nil
}
