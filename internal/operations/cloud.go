package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/PaNasMs/module-files/internal/cloudfs"
	"github.com/PaNasMs/module-files/internal/localfs"
	"os"
	"path"
)

func Cloud(ctx context.Context, mode string, r Request, cloud *cloudfs.Client, local *localfs.Engine, progress func(float64)) (any, error) {
	transfer := &cloudfs.Transfer{Cloud: cloud, Local: local, Progress: progress}
	if mode == "query" {
		entries, err := cloud.List(ctx, r.Target)
		return Listing{Path: r.Target, Entries: entries, Roots: []string{}, Places: []Place{}, TrashRoots: []string{}}, err
	}
	p := r.Params
	if mode == "plan" {
		var source any
		var destination any
		if r.Action == "file.mkdir" {
			_, err := transfer.Stat(ctx, p.Target)
			if !os.IsNotExist(err) {
				return nil, errors.New("destination already exists or is unavailable")
			}
		} else {
			item, err := transfer.Stat(ctx, p.Target)
			if err != nil {
				return nil, err
			}
			source = item.Revision
		}
		if p.Destination != "" {
			item, err := transfer.Stat(ctx, p.Destination)
			if err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			if err == nil {
				destination = item.Revision
				if p.ReplaceRevision != "" && p.ReplaceRevision != item.Revision {
					return nil, errors.New("destination changed; choose what to do again")
				}
			}
		}
		switch r.Action {
		case "file.mkdir", "file.copy", "file.move", "file.rename", "file.delete":
		default:
			return nil, errors.New("operation unavailable for cloud files")
		}
		raw, _ := json.Marshal([]any{r.Action, p, source, destination})
		hash := sha256.Sum256(raw)
		return map[string]any{"target": p.Target, "confirmation": p.Target, "fingerprint": hex.EncodeToString(hash[:]), "details": []string{p.Target, p.Destination}}, nil
	}
	if mode != "execute" {
		return nil, errors.New("unknown operation mode")
	}
	switch r.Action {
	case "file.copy", "file.move", "file.rename":
		return transfer.Run(ctx, p.Target, p.Destination, r.Action != "file.copy", p.Conflict, p.ReplaceRevision)
	case "file.mkdir":
		return map[string]string{"message": "Folder created"}, cloud.Mkdir(ctx, p.Target)
	case "file.delete":
		return map[string]string{"message": "File operation complete"}, deleteCloud(ctx, cloud, p.Target)
	}
	return nil, errors.New("operation unavailable for cloud files")
}
func deleteCloud(ctx context.Context, cloud *cloudfs.Client, target string) error {
	_, rel, err := cloudfs.Parse(target)
	if err != nil || rel == "" || path.Clean(rel) == "." {
		return errors.New("cannot remove an account root")
	}
	item, err := cloud.Stat(ctx, target)
	if err != nil {
		return err
	}
	if item.Directory {
		entries, err := cloud.List(ctx, target)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err = deleteCloud(ctx, cloud, entry.Path); err != nil {
				return err
			}
		}
	}
	return cloud.Delete(ctx, target, item.Directory)
}
