package archiveview

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/PaNasMs/module-files/internal/cloudfs"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const stagedIndex = -2

type batch struct {
	dir, tree string
	before    map[string]Entry
	hashes    map[string]string
	written   int64
}

func (s *Store) Within(ctx context.Context, source, destination string, operation func() (any, error)) (any, error) {
	if !cloudfs.IsArchive(source) || !cloudfs.IsArchive(destination) {
		return operation()
	}
	src, _, err := Parse(source)
	if err != nil {
		return nil, err
	}
	dst, _, err := Parse(destination)
	if err != nil {
		return nil, err
	}
	if src != dst {
		return operation()
	}
	a, _, err := s.archive(ctx, source)
	if err != nil {
		return nil, err
	}
	var result any
	err = s.batch(ctx, a, func() error { var e error; result, e = operation(); return e })
	return result, err
}

func (s *Store) batch(ctx context.Context, a *folderArchive, stage func() error) error {
	dir, err := os.MkdirTemp("", "panasms-edit-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	b := &batch{dir: dir, tree: filepath.Join(dir, "files"), before: a.entries, hashes: map[string]string{}}
	if err = os.Mkdir(b.tree, 0700); err != nil {
		return err
	}
	a.entries = maps.Clone(b.before)
	a.pending = b
	defer func() { a.pending = nil; a.entries = b.before; a.tarPath = "" }()
	if codec := tarCodec(suffix(a.source)); codec != "" {
		plain := filepath.Join(dir, "source.tar")
		if err = filter(ctx, dir, plain, io.NewSectionReader(a.file, 0, editLimit+1), codec, "-dc"); err != nil {
			return err
		}
		a.tarPath = plain
	}
	if err = stage(); err != nil {
		return err
	}
	return s.commit(ctx, a, b)
}

func (b *batch) stage(a *folderArchive, name, op string, in io.Reader, size int64, expected string) error {
	existing, exists := a.entries[name]
	if op == "delete" {
		if !exists {
			return os.ErrNotExist
		}
		for key := range a.entries {
			if key == name || strings.HasPrefix(key, name+"/") {
				delete(a.entries, key)
				delete(b.hashes, key)
			}
		}
		return os.RemoveAll(filepath.Join(b.tree, name))
	}
	if exists {
		entry, _ := a.entry("", name)
		if op == "mkdir" || existing.Directory || existing.Link || expected == "" || entry.Revision != expected {
			return errors.New("destination already exists or changed")
		}
	} else if expected != "" {
		return errors.New("destination changed; choose what to do again")
	}
	if size < 0 || size > editLimit-b.written {
		return errLimit
	}
	parent := path.Dir(name)
	if parent == "." {
		parent = ""
	}
	if p, ok := a.entries[parent]; !ok || !p.Directory {
		return os.ErrNotExist
	}
	staged := filepath.Join(b.tree, name)
	if err := os.MkdirAll(filepath.Dir(staged), 0777); err != nil {
		return err
	}
	row := Entry{Mode: existing.Mode, Name: name, Index: stagedIndex, Directory: op == "mkdir"}
	if row.Directory {
		if err := os.MkdirAll(staged, 0777); err != nil {
			return err
		}
	} else {
		h := sha256.New()
		if existing.Index == stagedIndex {
			if err := os.Remove(staged); err != nil {
				return err
			}
		}
		n, err := copyToFile(staged, io.TeeReader(in, h), size, 0666)
		if err != nil {
			return err
		}
		if n != size {
			return io.ErrUnexpectedEOF
		}
		row.Size = &size
		b.hashes[name] = fmt.Sprintf("%x", h.Sum(nil))
		b.written += n
	}
	a.entries[name] = row
	return nil
}

func (b *batch) changes(a *folderArchive) (removed, added []string, err error) {
	nonempty := map[string]bool{}
	for key := range a.entries {
		nonempty[path.Dir(key)] = true
	}
	for key, row := range b.before {
		after, ok := a.entries[key]
		if row.Index >= 0 && (!ok || after.Index == stagedIndex) {
			removed = append(removed, row.Name)
		}
	}
	for key, row := range a.entries {
		if row.Index == stagedIndex {
			added = append(added, key)
		} else if key != "" && row.Index == -1 && !nonempty[key] {
			if err = os.MkdirAll(filepath.Join(b.tree, key), 0777); err != nil {
				return nil, nil, err
			}
			added = append(added, key)
		}
	}
	return removed, added, nil
}

// Apply archived permissions only after every child has been staged.
func (s *Store) CopyMemberAttributes(source, destination string) {
	if !cloudfs.IsArchive(source) || !cloudfs.IsArchive(destination) {
		return
	}
	src, from, e := Parse(source)
	if e != nil {
		return
	}
	dst, to, e := Parse(destination)
	if e != nil || src != dst {
		return
	}
	a := s.opened[src]
	if a == nil || a.pending == nil {
		return
	}
	old, ok := a.entries[from]
	row := a.entries[to]
	if !ok || row.Index != stagedIndex {
		return
	}
	row.Mode = old.Mode
	row.Modified = old.Modified
	a.entries[to] = row
}
