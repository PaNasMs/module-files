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
	"regexp"
	"sort"
	"strings"
)

var trashContainer = regexp.MustCompile(`^\d{19,}-[a-z0-9_]{8}$`)

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
				if entry.Directory && trashContainer.MatchString(entry.Name) {
					items, _, e := localfs.List(entry.Path)
					if e == nil {
						listing.Entries = append(listing.Entries, items...)
					}
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
	return listing, err
}
func Plan(ctx context.Context, owner string, r Request) (map[string]any, error) {
	p := r.Params
	state := map[string]string{}
	switch r.Action {
	case "file.mkdir":
		if _, err := os.Lstat(p.Target); !os.IsNotExist(err) {
			return nil, errors.New("destination already exists")
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
	p := r.Params
	switch r.Action {
	case "file.mkdir":
		return map[string]string{"message": "Folder created"}, localfs.Mkdir(p.Target)
	case "file.delete":
		return map[string]string{"message": "File operation complete"}, localfs.Delete(ctx, p.Target)
	case "file.copy", "file.move", "file.rename", "file.restore":
		return engine.Transfer(ctx, p.Target, p.Destination, r.Action != "file.copy", p.Conflict, p.ReplaceRevision)
	case "file.trash":
		places, err := Places(ctx, owner)
		if err != nil {
			return nil, err
		}

		base, err := trashBase(p.Target, places.Roots)
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
