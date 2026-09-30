package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PaNasMs/module-files/internal/cloudfs"
	"github.com/PaNasMs/module-files/internal/localfs"
	"github.com/PaNasMs/module-files/internal/operations"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func operationMain() bool {
	if len(os.Args) < 2 || os.Args[1] != "operations" && os.Args[1] != "operations-user" {
		return false
	}
	if len(os.Args) != 4 {
		reply(nil, errors.New("invalid operation arguments"))
		return true
	}
	mode, owner := os.Args[2], os.Args[3]
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1048577))
	if err != nil || len(raw) > 1048576 {
		reply(nil, errors.New("invalid request"))
		return true
	}
	var r operations.Request
	if json.Unmarshal(raw, &r) != nil {
		reply(nil, errors.New("invalid request"))
		return true
	}
	permission := r.Action == "file.permissions" || strings.HasPrefix(r.Target, "permissions:") || strings.HasPrefix(r.Target, "permissions-selection:")
	a, err := user.Lookup(owner)
	if err != nil || a.Uid == "0" {
		reply(nil, errors.New("invalid user"))
		return true
	}
	uid, _ := strconv.Atoi(a.Uid)
	gid, _ := strconv.Atoi(a.Gid)
	if permission {
		groups, e := a.GroupIds()
		sudo, ge := user.LookupGroup("sudo")
		admin := false
		if e == nil && ge == nil {
			for _, g := range groups {
				admin = admin || g == sudo.Gid
			}
		}
		if !admin || os.Geteuid() != 0 {
			reply(nil, errors.New("administrator permissions required"))
			return true
		}
		value, e := permissionOperation(mode, r, raw)
		reply(value, e)
		return true
	}
	if os.Geteuid() == 0 {
		path, e := os.Executable()
		if e != nil {
			reply(nil, e)
			return true
		}
		cmd := exec.Command(path, "operations-user", mode, owner)
		cmd.Stdin = strings.NewReader(string(raw))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=" + a.HomeDir}
		groups, e := a.GroupIds()
		if e != nil {
			reply(nil, e)
			return true
		}
		ids := []uint32{}
		for _, g := range groups {
			n, e := strconv.Atoi(g)
			if e != nil {
				reply(nil, e)
				return true
			}
			ids = append(ids, uint32(n))
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: ids}, Pdeathsig: syscall.SIGTERM}
		if fd, e := strconv.Atoi(os.Getenv("PANASMS_CONTROL_FD")); e == nil && fd >= 3 {
			cmd.ExtraFiles = []*os.File{os.NewFile(uintptr(fd), "control")}
			cmd.Env = append(cmd.Env, "PANASMS_CONTROL_FD=3")
		}
		channel, e := grantChannel(owner)
		if e != nil {
			reply(nil, e)
			return true
		}
		defer channel.Close()
		cmd.Env = append(cmd.Env, fmt.Sprintf("PANASMS_GRANT_FD=%d", 3+len(cmd.ExtraFiles)))
		cmd.ExtraFiles = append(cmd.ExtraFiles, channel)
		if e = cmd.Run(); e != nil {
			reply(nil, errors.New("file worker failed"))
		}
		return true
	}
	if os.Geteuid() != uid {
		reply(nil, errors.New("invalid worker identity"))
		return true
	}
	mask, err := defaultUmask()
	if err != nil {
		reply(nil, err)
		return true
	}
	syscall.Umask(mask)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	engine := &localfs.Engine{Check: checkpoint, Capability: func(v bool) {
		if os.Getenv("PANASMS_CONTROL_FD") != "" {
			_ = json.NewEncoder(os.Stderr).Encode(map[string]bool{"cancellable": v})
		}
	}}
	var value any
	if cloudfs.IsCloud(r.Target) || cloudfs.IsCloud(r.Params.Target) || cloudfs.IsCloud(r.Params.Destination) {
		engine.Check = nil
		engine.Capability(true)
		defer engine.Capability(false)
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if checkpoint() != nil {
						cancel()
						return
					}
				}
			}
		}()
		cloud := &cloudfs.Client{Broker: cloudBroker()}
		value, err = operations.Cloud(ctx, mode, r, cloud, engine, func(percent float64) {
			_ = json.NewEncoder(os.Stderr).Encode(map[string]float64{"percent": percent})
		})
		reply(value, err)
		return true
	}
	switch mode {
	case "query":
		value, err = operations.Query(ctx, owner, r.Target)
	case "plan":
		value, err = operations.Plan(ctx, owner, r)
	case "execute":
		value, err = operations.Execute(ctx, owner, r, engine)
	default:
		err = errors.New("unknown operation mode")
	}
	reply(value, err)
	return true
}
func checkpoint() error {
	fd, err := strconv.Atoi(os.Getenv("PANASMS_CONTROL_FD"))
	if err != nil {
		return nil
	}
	if err = unix.SetNonblock(fd, true); err != nil {
		return err
	}
	var b [1]byte
	n, err := unix.Read(fd, b[:])
	if errors.Is(err, unix.EAGAIN) {
		return nil
	}
	if err != nil {
		return err
	}
	if n >= 0 {
		return context.Canceled
	}
	return nil
}
func reply(value any, err error) {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			value = map[string]bool{"cancelled": true}
		} else {
			value = map[string]string{"error": err.Error()}
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(value)
}
func defaultUmask() (int, error) {
	raw, err := os.ReadFile("/etc/login.defs")
	if os.IsNotExist(err) {
		return 0022, nil
	}
	if err != nil {
		return 0, err
	}
	mask := int64(0022)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(strings.SplitN(line, "#", 2)[0])
		if len(fields) == 0 || fields[0] != "UMASK" {
			continue
		}
		if len(fields) != 2 {
			return 0, errors.New("invalid UMASK")
		}
		mask, err = strconv.ParseInt(fields[1], 8, 16)
		if err != nil || mask < 0 || mask > 0777 {
			return 0, errors.New("invalid UMASK")
		}
	}
	return int(mask), nil
}
func permissionOperation(mode string, r operations.Request, raw []byte) (any, error) {
	if mode == "query" {
		if strings.HasPrefix(r.Target, "permissions:") {
			return localfs.Permissions(strings.TrimPrefix(r.Target, "permissions:"))
		}
		var paths []string
		if json.Unmarshal([]byte(strings.TrimPrefix(r.Target, "permissions-selection:")), &paths) != nil || len(paths) == 0 || len(paths) > 100 {
			return nil, errors.New("invalid selection")
		}
		items := []localfs.Permission{}
		for _, p := range paths {
			item, e := localfs.Permissions(p)
			if e != nil {
				return nil, e
			}
			items = append(items, item)
		}
		names := func(path string) []string {
			out := []string{}
			raw, _ := os.ReadFile(path)
			for _, line := range strings.Split(string(raw), "\n") {
				if name, _, ok := strings.Cut(line, ":"); ok {
					out = append(out, name)
				}
			}
			return out
		}
		return map[string]any{"items": items, "users": names("/etc/passwd"), "groups": names("/etc/group")}, nil
	}
	var body struct{ Params localfs.PermissionChange }
	if json.Unmarshal(raw, &body) != nil {
		return nil, errors.New("invalid permissions")
	}
	if mode != "plan" && mode != "execute" {
		return nil, errors.New("unknown mode")
	}
	if err := localfs.ChangePermissions(body.Params, mode == "execute"); err != nil {
		return nil, err
	}
	target := body.Params.Target
	if len(body.Params.Items) > 0 {
		target = body.Params.Items[0].Target
	}
	if mode == "plan" {
		return map[string]any{"target": target, "confirmation": target, "fingerprint": fmt.Sprintf("%x", sha256.Sum256(raw)), "details": []string{target}}, nil
	}
	return map[string]string{"message": "Ownership and permissions updated"}, nil
}
