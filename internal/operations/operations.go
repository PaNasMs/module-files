package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PaNasMs/module-files/internal/localfs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
)

// Replaced in tests, which cannot enumerate real mounts.
var places = Places

type Params struct {
	Target          string           `json:"target"`
	Destination     string           `json:"destination"`
	ReplaceRevision string           `json:"replace_revision"`
	Conflict        localfs.Conflict `json:"conflict"`
}
type Request struct {
	Action string `json:"action"`
	Params Params `json:"params"`
	Target string `json:"target"`
	View   string `json:"view"`
}
type Place struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Network bool   `json:"network,omitempty"`
}
type Listing struct {
	Roots      []string        `json:"roots"`
	Places     []Place         `json:"places"`
	TrashRoots []string        `json:"trashRoots"`
	Path       string          `json:"path,omitempty"`
	Entries    []localfs.Entry `json:"entries"`
	FreeBytes  uint64          `json:"freeBytes"`
}
type mount struct{ Target, Source, Fstype string }

func Places(ctx context.Context, owner string) (Listing, error) {
	a, err := user.Lookup(owner)
	if err != nil {
		return Listing{}, err
	}
	listing := Listing{Roots: []string{"/"}, Places: []Place{{"/", "System", "device", false}}, TrashRoots: []string{}, Entries: []localfs.Entry{}}
	if a.HomeDir != "/" {
		listing.Roots = append(listing.Roots, a.HomeDir)
		listing.Places = append(listing.Places, Place{a.HomeDir, "Home folder", "home", false})
	}
	raw, err := exec.CommandContext(ctx, "findmnt", "--json", "--list", "--real", "-o", "TARGET,SOURCE,FSTYPE").Output()
	if err != nil {
		return listing, err
	}
	var data struct{ Filesystems []mount }
	if err = json.Unmarshal(raw, &data); err != nil {
		return listing, err
	}
	names := mountNames(ctx)
	for _, m := range data.Filesystems {
		if m.Target == a.HomeDir || !strings.HasPrefix(m.Target, "/srv/") && !strings.HasPrefix(m.Target, "/mnt/") && !strings.HasPrefix(m.Target, "/media/") || m.Fstype == "autofs" {
			continue
		}
		network := m.Fstype == "nfs" || m.Fstype == "nfs4" || m.Fstype == "cifs" || m.Fstype == "smb3"
		if strings.HasPrefix(m.Source, "/dev/") {
			if _, e := os.Stat(strings.Split(m.Source, "[")[0]); e != nil {
				continue
			}
		}
		name := names[m.Target]
		if network {
			name = filepath.Base(strings.TrimSuffix(m.Source, "/"))
		}
		if name == "" {
			name = filepath.Base(m.Target)
		}
		listing.Roots = append(listing.Roots, m.Target)
		listing.Places = append(listing.Places, Place{m.Target, name, "device", network})
	}
	sort.Slice(listing.Roots, func(i, j int) bool { return len(listing.Roots[i]) > len(listing.Roots[j]) })
	for _, root := range listing.Roots {
		p := filepath.Join(root, ".panasms-trash-"+a.Uid)
		s, e := os.Lstat(p)
		if e == nil && s.IsDir() {
			if _, _, e = localfs.List(p); e == nil {
				listing.TrashRoots = append(listing.TrashRoots, p)
			}
		}
	}
	listing.Places = append(listing.Places, Place{"trash:", "Trash", "trash", false})
	return listing, nil
}
func Query(ctx context.Context, owner, path string) (Listing, error) {
	listing, err := Places(ctx, owner)
	if err != nil || path == "" {
		return listing, err
	}
	listing.Path = path
	if path == "trash:" {
		for _, root := range listing.TrashRoots {
			entries, _, e := localfs.List(root)
			if e != nil {
				continue
			}
			for _, entry := range entries {
				if entry.Directory && localfs.TrashContainer(entry.Name) {
					listing.Entries = append(listing.Entries, containerEntries(entry.Path, listing.Roots)...)
				} else {
					listing.Entries = append(listing.Entries, entry)
				}
				if len(listing.Entries) > 10000 {
					return listing, errors.New("too many items in trash")
				}
			}
		}
		return listing, nil
	}
	listing.Entries, listing.FreeBytes, err = localfs.List(path)
	if err == nil {
		// Inside a trashed folder every entry keeps its place relative to the folder's origin.
		original, deleted := trashOrigin(path, listing.Roots)
		for i := range listing.Entries {
			listing.Entries[i].Deleted = deleted
			if original != "" {
				listing.Entries[i].Original = original + "/" + listing.Entries[i].Name
			}
		}
	}
	return listing, err
}

// containerEntries lists the item kept in one trash container, annotated with where it
// came from. The note itself is hidden unless it is the only thing left.
func containerEntries(container string, roots []string) []localfs.Entry {
	all, _, err := localfs.List(container)
	if err != nil {
		return nil
	}
	items := []localfs.Entry{}
	for _, entry := range all {
		if !localfs.TrashMetadata(entry.Name) {
			items = append(items, entry)
		}
	}
	if len(items) == 0 {
		items = all
	}
	for i := range items {
		items[i].Original, items[i].Deleted = trashOrigin(items[i].Path, roots)
	}
	return items
}

// trashOrigin reports where a path inside the caller's trash came from and when its
// container was created. The recorded path is accepted only if it names the same item
// on the storage root that holds this trash; anything else reads as "unknown".
func trashOrigin(path string, roots []string) (string, float64) {
	marker := "/" + localfs.TrashName() + "/"
	i := strings.Index(path, marker)
	if i < 0 || filepath.Clean(path) != path {
		return "", 0
	}
	base := path[:i]
	if base == "" {
		base = "/"
	}
	known := false
	for _, root := range roots {
		known = known || filepath.Clean(root) == base
	}
	parts := strings.Split(path[i+len(marker):], "/")
	if !known || len(parts) < 2 || !localfs.TrashContainer(parts[0]) {
		return "", 0
	}
	deleted := localfs.TrashDeleted(parts[0])
	trash := path[:i+len(marker)-1]
	original := localfs.TrashOriginal(trash + "/" + parts[0])
	if original == "" || filepath.Base(original) != parts[1] || original == trash || strings.HasPrefix(original, trash+"/") {
		return "", deleted
	}
	if root, err := trashBase(original, roots); err != nil || filepath.Clean(root) != base {
		return "", deleted
	}
	return filepath.Join(append([]string{original}, parts[2:]...)...), deleted
}

// restoreDefault fills in the destination of a restore that names none: the place the
// item was trashed from.
func restoreDefault(ctx context.Context, owner string, r *Request) error {
	if r.Action != "file.restore" || r.Params.Destination != "" {
		return nil
	}
	listing, err := places(ctx, owner)
	if err != nil {
		return err
	}
	original, _ := trashOrigin(r.Params.Target, listing.Roots)
	if original == "" {
		return errors.New("original location is unknown; choose where to restore this item")
	}
	r.Params.Destination = original
	return nil
}
func Plan(ctx context.Context, owner string, r Request) (map[string]any, error) {
	if err := restoreDefault(ctx, owner, &r); err != nil {
		return nil, err
	}
	p := r.Params
	state := map[string]string{}
	switch r.Action {
	case "file.mkdir":
		// Only a successful lookup proves a conflict; an unreadable parent is an access error.
		if _, err := os.Lstat(p.Target); err == nil {
			return nil, errors.New("destination already exists")
		} else if os.IsPermission(err) {
			return nil, errors.New("permission denied")
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		rev, err := localfs.Revision(filepath.Dir(p.Target))
		if err != nil {
			return nil, err
		}
		state["parent"] = rev
	case "file.copy", "file.move", "file.rename", "file.restore", "file.delete", "file.trash":
		rev, err := localfs.Revision(p.Target)
		if err != nil {
			return nil, err
		}
		state["source"] = rev
	default:
		return nil, errors.New("unknown file operation")
	}
	if p.Destination != "" {
		if p.Target == p.Destination || strings.HasPrefix(p.Destination, p.Target+"/") || strings.HasPrefix(p.Target, p.Destination+"/") {
			return nil, errors.New("source and destination must not contain each other")
		}
		rev, err := localfs.Revision(p.Destination)
		if err == nil {
			state["destination"] = rev
			if p.ReplaceRevision != "" && rev != p.ReplaceRevision {
				return nil, errors.New("destination changed; choose what to do again")
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		rev, err = localfs.Revision(filepath.Dir(p.Destination))
		if err != nil {
			return nil, err
		}
		state["parent"] = rev
	}
	raw, _ := json.Marshal([]any{r.Action, p, state})
	hash := sha256.Sum256(raw)
	return map[string]any{"target": p.Target, "confirmation": p.Target, "fingerprint": hex.EncodeToString(hash[:]), "details": []string{p.Target, p.Destination}}, nil
}
func Execute(ctx context.Context, owner string, r Request, engine *localfs.Engine) (any, error) {
	if err := restoreDefault(ctx, owner, &r); err != nil {
		return nil, err
	}
	p := r.Params
	switch r.Action {
	case "file.mkdir":
		return map[string]string{"message": "Folder created"}, localfs.Mkdir(p.Target)
	case "file.delete":
		err := localfs.Delete(ctx, p.Target)
		if err == nil {
			// Best effort: a leftover empty container is harmless and stays invisible.
			_ = localfs.TrashCleanup(p.Target)
		}
		return map[string]string{"message": "File operation complete"}, err
	case "file.copy", "file.move", "file.rename", "file.restore":
		result, err := engine.Transfer(ctx, p.Target, p.Destination, r.Action != "file.copy", p.Conflict, p.ReplaceRevision)
		if err == nil && r.Action != "file.copy" {
			_ = localfs.TrashCleanup(p.Target)
		}
		return result, err
	case "file.trash":
		listing, err := places(ctx, owner)
		if err != nil {
			return nil, err
		}

		base, err := trashBase(p.Target, listing.Roots)
		if err != nil {
			return nil, err
		}
		destination, err := localfs.Trash(ctx, p.Target, base, engine)
		if err != nil {
			return nil, err
		}
		return map[string]string{"message": "Moved to trash", "path": destination, "original": p.Target}, nil
	}
	return nil, fmt.Errorf("unknown operation %s", r.Action)
}
func mountNames(ctx context.Context) map[string]string {
	type device struct {
		Name, Path, Type, Model, Label string
		Mountpoints                    []string
		Children                       []json.RawMessage
	}
	names := map[string]string{}
	raw, err := exec.CommandContext(ctx, "lsblk", "--json", "--output", "NAME,PATH,TYPE,MODEL,LABEL,MOUNTPOINTS").Output()
	if err != nil {
		return names
	}
	var data struct{ Blockdevices []json.RawMessage }
	if json.Unmarshal(raw, &data) != nil {
		return names
	}
	aliases := map[string]string{}
	paths, _ := filepath.Glob("/dev/md/*")
	for _, p := range paths {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			aliases[real] = filepath.Base(p)
		}
	}
	var visit func(json.RawMessage, string, bool)
	visit = func(raw json.RawMessage, model string, multiple bool) {
		var d device
		if json.Unmarshal(raw, &d) != nil {
			return
		}
		if strings.HasPrefix(d.Type, "raid") {
			model = aliases[d.Path]
			if model == "" {
				model = d.Name
			}
			multiple = false
		} else if strings.TrimSpace(d.Model) != "" {
			model = strings.TrimSpace(d.Model)
		}
		name := model
		if name == "" {
			name = d.Label
		}
		if name == "" {
			name = d.Name
		}
		if model != "" && multiple {
			label := d.Label
			if label == "" {
				label = d.Name
			}
			name += " · " + label
		}
		for _, p := range d.Mountpoints {
			if p != "" {
				names[p] = name
			}
		}
		for _, child := range d.Children {
			visit(child, model, len(d.Children) > 1)
		}
	}
	for _, d := range data.Blockdevices {
		visit(d, "", false)
	}
	return names
}

func trashBase(path string, roots []string) (string, error) {
	path = filepath.Clean(path)
	for _, root := range roots {
		if path == filepath.Clean(root) {
			return "", errors.New("cannot trash a storage root")
		}
	}
	for _, root := range roots {
		if strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/") {
			return root, nil
		}
	}
	return "", errors.New("cannot trash a storage root")
}
