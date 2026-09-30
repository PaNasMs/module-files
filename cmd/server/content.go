package main

import (
	"context"
	"fmt"
	"github.com/PaNasMs/module-files/internal/cloudfs"
	"github.com/PaNasMs/module-files/internal/localfs"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

func contentMain() bool {
	if len(os.Args) < 2 || os.Args[1] != "content" && os.Args[1] != "content-user" {
		return false
	}
	if len(os.Args) != 7 {
		fmt.Fprintln(os.Stderr, "invalid content request")
		os.Exit(1)
	}
	mode, owner, path := os.Args[2], os.Args[3], os.Args[4]
	a, err := user.Lookup(owner)
	if err != nil || a.Uid == "0" {
		os.Exit(1)
	}
	uid, _ := strconv.Atoi(a.Uid)
	gid, _ := strconv.Atoi(a.Gid)
	if os.Geteuid() == 0 {
		exe, e := os.Executable()
		if e != nil {
			os.Exit(1)
		}
		args := append([]string{"content-user"}, os.Args[2:]...)
		cmd := exec.Command(exe, args...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=" + a.HomeDir, "GOMEMLIMIT=256MiB"}
		groups, e := a.GroupIds()
		if e != nil {
			os.Exit(1)
		}
		ids := []uint32{}
		for _, g := range groups {
			n, e := strconv.Atoi(g)
			if e != nil {
				os.Exit(1)
			}
			ids = append(ids, uint32(n))
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: ids}, Pdeathsig: syscall.SIGTERM}
		if cloudfs.IsCloud(path) {
			channel, e := grantChannel(owner)
			if e != nil {
				os.Exit(1)
			}
			defer channel.Close()
			cmd.ExtraFiles = []*os.File{channel}
			cmd.Env = append(cmd.Env, "PANASMS_GRANT_FD=3")
		}
		if e = cmd.Run(); e != nil {
			os.Exit(1)
		}
		return true
	}
	if os.Geteuid() != uid {
		os.Exit(1)
	}
	mask, err := defaultUmask()
	if err != nil {
		os.Exit(1)
	}
	syscall.Umask(mask)
	if cloudfs.IsCloud(path) {
		cloud := &cloudfs.Client{Broker: cloudBroker()}
		ctx := context.Background()
		switch mode {
		case "download":
			var item localfs.Entry
			item, err = cloud.Stat(ctx, path)
			if err == nil && !item.Directory && item.Size >= 0 {
				fmt.Fprintf(os.Stdout, "OK %d\n", item.Size)
				err = cloud.Read(ctx, path, os.Stdout)
			} else {
				err = fmt.Errorf("cloud file cannot be downloaded")
			}
		case "upload":
			var size int64
			size, err = strconv.ParseInt(os.Args[5], 10, 64)
			if err == nil {
				err = cloud.Upload(ctx, path, os.Stdin, size, os.Args[6])
			}
		default:
			err = fmt.Errorf("cloud thumbnail is unavailable")
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return true
	}
	switch mode {
	case "thumbnail":
		var data []byte
		data, err = localfs.Thumbnail(path)
		if err == nil {
			fmt.Fprintf(os.Stdout, "OK %d\n", len(data))
			_, err = os.Stdout.Write(data)
		}
	case "download":
		var f *os.File
		f, err = localfs.Open(path)
		if err == nil {
			var s os.FileInfo
			s, err = f.Stat()
			defer f.Close()
			if err == nil {
				fmt.Fprintf(os.Stdout, "OK %d\n", s.Size())
				err = localfs.StreamFile(f, os.Stdout)
			}
		}
	case "upload":
		var size int64
		size, err = strconv.ParseInt(os.Args[5], 10, 64)
		if err == nil {
			err = (&localfs.Engine{}).Upload(context.Background(), path, os.Stdin, size, os.Args[6])
		}
	default:
		err = fmt.Errorf("unknown content mode")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return true
}
