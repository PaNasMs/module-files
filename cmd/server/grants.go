package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/PaNasMs/module-sdk/external"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type grantReply struct {
	Access external.Access `json:"access"`
	Error  string          `json:"error,omitempty"`
}

func grantChannel(owner string) (*os.File, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parent := os.NewFile(uintptr(fds[0]), "grant-parent")
	child := os.NewFile(uintptr(fds[1]), "grant-child")
	go func() {
		defer parent.Close()
		conn, err := net.FileConn(parent)
		if err != nil {
			return
		}
		defer conn.Close()
		dec := json.NewDecoder(conn)
		client := http.Client{Timeout: 45 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", "/run/panasms-modules/files.sock")
		}}}
		for {
			var req struct {
				Grant string `json:"grant"`
			}
			if dec.Decode(&req) != nil || len(req.Grant) > 128 {
				return
			}
			body, _ := json.Marshal(map[string]string{"grant": req.Grant, "owner": owner})
			response, err := client.Post("http://module/private-grant", "application/json", bytes.NewReader(body))
			reply := grantReply{Error: "Cloud access service is unavailable"}
			if err == nil {
				if response.StatusCode == 200 {
					var decoded grantReply
					if json.NewDecoder(io.LimitReader(response.Body, 32768)).Decode(&decoded) == nil {
						reply = decoded
					}
				}
				response.Body.Close()
			}
			if json.NewEncoder(conn).Encode(reply) != nil {
				return
			}
		}
	}()
	return child, nil
}
func cloudBroker() func(context.Context, string) (external.Access, error) {
	fd, err := strconv.Atoi(os.Getenv("PANASMS_GRANT_FD"))
	if err != nil {
		return func(context.Context, string) (external.Access, error) {
			return external.Access{}, errors.New("cloud access channel unavailable")
		}
	}
	f := os.NewFile(uintptr(fd), "grants")
	conn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return func(context.Context, string) (external.Access, error) { return external.Access{}, err }
	}
	var mu sync.Mutex
	return func(ctx context.Context, grant string) (external.Access, error) {
		mu.Lock()
		defer mu.Unlock()
		deadline := time.Now().Add(45 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetDeadline(deadline)
		if err := json.NewEncoder(conn).Encode(map[string]string{"grant": grant}); err != nil {
			return external.Access{}, errors.New("cloud access channel unavailable")
		}
		var reply grantReply
		if json.NewDecoder(io.LimitReader(conn, 32768)).Decode(&reply) != nil {
			return external.Access{}, errors.New("invalid cloud permission response")
		}
		if reply.Error != "" {
			return external.Access{}, errors.New(reply.Error)
		}
		return reply.Access, nil
	}
}
