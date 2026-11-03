package archiveview

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/PaNasMs/module-files/internal/cloudfs"
	"github.com/PaNasMs/module-files/internal/localfs"
	"golang.org/x/sys/unix"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const cloudLimit = 64 << 20
const editLimit = 1 << 30

var errReadOnly = errors.New("This archive is read-only")
var errPath = errors.New("Invalid archive member path")

type Store struct {
	*cloudfs.Client
	opened map[string]*folderArchive
}
type folderArchive struct {
	remoteVersion              *cloudfs.ArchiveRevision
	pending                    *batch
	tarPath                    string
	file                       *os.File
	source, revision, baseline string
	entries                    map[string]Entry
	readonly                   bool
}

func Parse(value string) (string, string, error) {
	encoded, member, _ := strings.Cut(strings.TrimPrefix(value, "arc:"), "/")
	source, err := url.PathUnescape(encoded)
	canonical := strings.ReplaceAll(url.QueryEscape(source), "+", "%20")
	if len(value) > 8192 || encoded != canonical || !cloudfs.IsArchive(value) || err != nil || suffix(source) == "" || (!filepath.IsAbs(source) && !cloudfs.IsRemote(source)) || member != "" && !validMember(member) {
		return "", "", errPath
	}
	return source, member, nil
}
func validMember(s string) bool {
	return fs.ValidPath(s) && s != "." && !strings.ContainsAny(s, "\\\x00\n\r")
}
func (s *Store) Close() {
	for _, a := range s.opened {
		a.file.Close()
	}
}
func (s *Store) load(ctx context.Context, source string) (*folderArchive, error) {
	var f *os.File
	var rev string
	var remote *cloudfs.ArchiveRevision
	var err error
	if cloudfs.IsRemote(source) {
		item, e := s.Stat(ctx, source)
		if e != nil {
			return nil, e
		}
		if item.Size < 0 || item.Size > cloudLimit || item.Directory {
			return nil, errors.New("Cloud archive exceeds the 64 MiB preview limit")
		}
		rev = item.Revision
		if cloudfs.IsCloud(source) {
			remote, _ = s.Client.ArchiveRevision(ctx, source, item.ProviderID)
		}
		f, _, err = cloudfs.Spool(func(w io.Writer) error { return s.Read(ctx, source, w) }, cloudLimit)
	} else {
		rev, err = localfs.Revision(source)
		if err == nil {
			f, err = localfs.Open(source)
		}
	}
	if err != nil {
		return nil, err
	}
	a := &folderArchive{remoteVersion: remote, file: f, source: source, revision: rev, baseline: rev}
	if err = a.index(ctx); err != nil {
		f.Close()
		return nil, err
	}
	if cloudfs.IsCloud(source) && remote == nil {
		a.readonly = true
	}
	if !cloudfs.IsRemote(source) {
		info, e := f.Stat()
		if e != nil {
			f.Close()
			return nil, e
		}
		st := info.Sys().(*syscall.Stat_t)
		a.readonly = a.readonly || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Mode&07000 != 0 ||
			unix.Access(source, unix.W_OK) != nil || unix.Access(filepath.Dir(source), unix.W_OK|unix.X_OK) != nil ||
			strings.Contains(source, "/"+localfs.TrashName()+"/")
	}
	if cloudfs.IsArchive(source) && s.ReadOnly(ctx, source) {
		a.readonly = true
	}
	return a, nil
}
func (a *folderArchive) index(ctx context.Context) error {
	list, err := List(ctx, a.file, a.source)
	if err != nil {
		return err
	}
	a.entries = map[string]Entry{"": {Directory: true, Index: -1}}
	a.readonly = suffix(a.source) == ".rar"
	for _, row := range list {
		name := strings.TrimSuffix(strings.TrimPrefix(row.Name, "./"), "/")
		if (name == "." || name == "") && row.Directory {
			continue
		}
		if !validMember(name) {
			return errPath
		}
		// ZIP readers disagree on legacy encodings and on backslash separators.
		// Ambiguous paths must never be used as update selectors.
		prior, exists := a.entries[name]
		if exists && (prior.Index >= 0 || !row.Directory) {
			return errPath
		}
		a.entries[name] = row
		if row.Encrypted {
			a.readonly = true
		}
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if entry, ok := a.entries[parent]; ok {
				if !entry.Directory {
					return errPath
				}
				break
			}
			a.entries[parent] = Entry{Name: parent, Directory: true, Index: -1}
		}
	}
	return nil
}
func (s *Store) archive(ctx context.Context, value string) (*folderArchive, string, error) {
	if _, err := logicalPath(value); err != nil {
		return nil, "", err
	}
	source, name, err := Parse(value)
	if err != nil {
		return nil, "", err
	}
	if a := s.opened[source]; a != nil {
		return a, name, nil
	}
	a, err := s.load(ctx, source)
	if err != nil {
		return nil, "", err
	}
	if s.opened == nil {
		s.opened = map[string]*folderArchive{}
	}
	s.opened[source] = a
	return a, name, nil
}
func (a *folderArchive) entry(value, name string) (localfs.Entry, error) {
	row, ok := a.entries[name]
	if !ok {
		return localfs.Entry{}, os.ErrNotExist
	}
	var size int64
	if row.Size != nil {
		size = *row.Size
	} else if !row.Directory {
		size = -1
	}
	return localfs.Entry{Mode: row.Mode, Path: value, Name: path.Base(name), Directory: row.Directory, Size: size, Link: row.Link, Modified: row.Modified, Revision: fmt.Sprintf("%x", sha256.Sum256([]byte(a.baseline+"\x00"+name)))}, nil
}
func (s *Store) Stat(ctx context.Context, value string) (localfs.Entry, error) {
	if !cloudfs.IsArchive(value) {
		return s.Client.Stat(ctx, value)
	}
	a, name, err := s.archive(ctx, value)
	if err != nil {
		return localfs.Entry{}, err
	}
	return a.entry(value, name)
}
func (s *Store) List(ctx context.Context, value string) ([]localfs.Entry, error) {
	if !cloudfs.IsArchive(value) {
		return s.Client.List(ctx, value)
	}
	a, name, err := s.archive(ctx, value)
	if err != nil {
		return nil, err
	}
	if row, ok := a.entries[name]; !ok || !row.Directory {
		return nil, os.ErrNotExist
	}
	prefix := name
	if name != "" {
		prefix += "/"
	}
	result := []localfs.Entry{}
	for key := range a.entries {
		relative, ok := strings.CutPrefix(key, prefix)
		if !ok || relative == "" || strings.Contains(relative, "/") {
			continue
		}
		e, _ := a.entry(value+"/"+relative, key)
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
func (s *Store) Writable(ctx context.Context, value string) error {
	if !cloudfs.IsArchive(value) {
		return nil
	}
	a, name, err := s.archive(ctx, value)
	if err != nil {
		return err
	}
	if name == "" || a.readonly {
		return errReadOnly
	}
	return nil
}
func (s *Store) Read(ctx context.Context, value string, out io.Writer) error {
	if !cloudfs.IsArchive(value) {
		return s.Client.Read(ctx, value, out)
	}
	a, name, err := s.archive(ctx, value)
	if err != nil {
		return err
	}
	return a.read(ctx, name, out)
}
func (s *Store) ReadOnly(ctx context.Context, value string) bool {
	a, _, err := s.archive(ctx, value)
	return err != nil || a.readonly
}

func (s *Store) CheckOperation(ctx context.Context, mode, action, target, destination string) error {
	if mode == "query" {
		return nil
	}
	if cloudfs.IsArchive(target) || cloudfs.IsArchive(destination) {
		src, e := logicalPath(target)
		if e != nil {
			return e
		}
		dst, e := logicalPath(destination)
		if e != nil {
			return e
		}
		if dst != "" && (src == dst || strings.HasPrefix(dst, src+"/") || strings.HasPrefix(src, dst+"/")) {
			return errors.New("source and destination must not contain each other")
		}
	}
	for _, v := range []string{target, destination} {
		if !cloudfs.IsArchive(v) {
			continue
		}
		_, member, e := Parse(v)
		if e != nil {
			return e
		}
		if member == "" {
			return errPath
		}
		if v == destination || action != "file.copy" {
			if e = s.Writable(ctx, v); e != nil {
				return e
			}
		}
	}
	return nil
}

func logicalPath(value string) (string, error) {
	members := []string{}
	for cloudfs.IsArchive(value) {
		if len(members) >= 4 {
			return "", errLimit
		}
		source, member, err := Parse(value)
		if err != nil {
			return "", err
		}
		members = append(members, member)
		value = source
	}
	for i := len(members) - 1; i >= 0; i-- {
		if members[i] != "" {
			value += "/" + members[i]
		}
	}
	return value, nil
}
