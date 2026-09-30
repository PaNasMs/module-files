package localfs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"os/user"
	"strconv"
	"strings"
)

type Permission struct {
	Target     string `json:"target"`
	Directory  bool   `json:"directory"`
	Owner      string `json:"owner"`
	Group      string `json:"group"`
	Mode       string `json:"mode"`
	Revision   string `json:"revision"`
	ACL        bool   `json:"acl"`
	DefaultACL bool   `json:"defaultAcl"`
}
type PermissionItem struct {
	Target   string `json:"target"`
	Revision string `json:"revision"`
}
type PermissionChange struct {
	Target   string           `json:"target"`
	Revision string           `json:"revision"`
	Owner    string           `json:"owner"`
	Group    string           `json:"group"`
	Mode     string           `json:"mode"`
	Items    []PermissionItem `json:"items"`
	Mask     uint32           `json:"mask"`
	Bits     uint32           `json:"bits"`
}

func permissionFile(path string) (*os.File, error) {
	if !strings.HasPrefix(path, "/home/") && !strings.HasPrefix(path, "/srv/") && !strings.HasPrefix(path, "/mnt/") && !strings.HasPrefix(path, "/media/") {
		return nil, errors.New("choose a home or data folder; system folders are protected")
	}
	t, e := resolve(path)
	if e != nil {
		return nil, e
	}
	defer t.close()
	f, e := t.open(unix.O_RDONLY, 0)
	if e != nil {
		return nil, e
	}
	var s unix.Stat_t
	var fs unix.Statfs_t
	if unix.Fstat(int(f.Fd()), &s) != nil || (!regular(s) && !isDir(s)) || unix.Fstatfs(int(f.Fd()), &fs) != nil {
		f.Close()
		return nil, errors.New("choose a regular file or folder")
	}
	if fs.Type != unix.EXT4_SUPER_MAGIC && fs.Type != unix.XFS_SUPER_MAGIC && fs.Type != unix.BTRFS_SUPER_MAGIC {
		f.Close()
		return nil, errors.New("permissions can be edited only on local Linux filesystems")
	}
	return f, nil
}
func permissionRevision(s unix.Stat_t) string {
	raw, _ := json.Marshal([]any{s.Dev, s.Ino, s.Uid, s.Gid, s.Mode, s.Ctim})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func Permissions(path string) (Permission, error) {
	f, e := permissionFile(path)
	if e != nil {
		return Permission{}, e
	}
	defer f.Close()
	var s unix.Stat_t
	if e = unix.Fstat(int(f.Fd()), &s); e != nil {
		return Permission{}, e
	}
	owner := strconv.Itoa(int(s.Uid))
	if u, e := user.LookupId(owner); e == nil {
		owner = u.Username
	}
	group := strconv.Itoa(int(s.Gid))
	if g, e := user.LookupGroupId(group); e == nil {
		group = g.Name
	}
	_, acl := unix.Fgetxattr(int(f.Fd()), "system.posix_acl_access", nil)
	_, defaultACL := unix.Fgetxattr(int(f.Fd()), "system.posix_acl_default", nil)
	return Permission{path, isDir(s), owner, group, fmt.Sprintf("%04o", s.Mode&07777), permissionRevision(s), acl == nil, defaultACL == nil}, nil
}
func ChangePermissions(p PermissionChange, apply bool) error {
	if p.Mask&^uint32(02777) != 0 || p.Bits&^p.Mask != 0 {
		return errors.New("invalid permissions")
	}
	uid, gid := -1, -1
	if p.Owner != "" {
		u, e := user.Lookup(p.Owner)
		if e != nil {
			u, e = user.LookupId(p.Owner)
		}
		if e != nil {
			return errors.New("unknown owner")
		}
		uid, _ = strconv.Atoi(u.Uid)
	}
	if p.Group != "" {
		g, e := user.LookupGroup(p.Group)
		if e != nil {
			g, e = user.LookupGroupId(p.Group)
		}
		if e != nil {
			return errors.New("unknown group")
		}
		gid, _ = strconv.Atoi(g.Gid)
	}
	items := p.Items
	single := items == nil
	var direct uint64
	var err error
	if single {
		items = []PermissionItem{{p.Target, p.Revision}}
		direct, err = strconv.ParseUint(p.Mode, 8, 12)
		if err != nil || len(p.Mode) != 4 || direct > 03777 {
			return errors.New("invalid permissions")
		}
	}
	if len(items) == 0 || len(items) > 100 {
		return errors.New("invalid selection")
	}
	type change struct {
		f    *os.File
		mode uint32
	}
	changes := []change{}
	defer func() {
		for _, c := range changes {
			c.f.Close()
		}
	}()
	seen := map[string]bool{}
	for _, i := range items {
		if seen[i.Target] {
			return errors.New("duplicate selection")
		}
		seen[i.Target] = true
		f, e := permissionFile(i.Target)
		if e != nil {
			return e
		}
		changes = append(changes, change{f: f})
		var s unix.Stat_t
		if e = unix.Fstat(int(f.Fd()), &s); e != nil {
			return e
		}
		if permissionRevision(s) != i.Revision {
			return errors.New("folder permissions changed; reopen the dialog and try again")
		}
		mask := p.Mask
		if !isDir(s) {
			mask &^= 02000
		}
		mode := (s.Mode & 07777 &^ mask) | (p.Bits & mask)
		if single {
			mode = uint32(direct)
		}
		if mode&04000 != 0 {
			return errors.New("setuid files are protected")
		}
		changes[len(changes)-1].mode = mode
	}
	if apply {
		for _, c := range changes {
			if uid != -1 || gid != -1 {
				if err = unix.Fchown(int(c.f.Fd()), uid, gid); err != nil {
					return err
				}
			}
			if err = unix.Fchmod(int(c.f.Fd()), c.mode); err != nil {
				return err
			}
		}
	}
	return nil
}
