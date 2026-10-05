package cloudfs

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PaNasMs/module-files/internal/localfs"
	"github.com/PaNasMs/module-sdk/external"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"
)

type Broker func(context.Context, string) (external.Access, error)
type Client struct {
	Broker Broker
	Binary string
	// DropboxAPI overrides the Dropbox API origin in tests.
	DropboxAPI string
	run        func(context.Context, string, []string, io.Reader, io.Writer) error
}

var grantPattern = regexp.MustCompile(`^[a-f0-9]{32,64}$`)

func IsCloud(value string) bool { return strings.HasPrefix(value, "cloud:") }
func Parse(value string) (string, string, error) {
	grant, rel, _ := strings.Cut(strings.TrimPrefix(value, "cloud:"), "/")
	if !IsCloud(value) || !grantPattern.MatchString(grant) || strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") || rel != "" && path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", "", errors.New("invalid cloud location")
	}
	return grant, rel, nil
}
func Join(base, name string) string { return strings.TrimSuffix(base, "/") + "/" + name }
func Parent(value string) string {
	grant, rel, _ := Parse(value)
	parent := path.Dir(rel)
	if parent == "." {
		parent = ""
	}
	if parent == "" {
		return "cloud:" + grant
	}
	return "cloud:" + grant + "/" + parent
}
func (c *Client) command(ctx context.Context, value string, args []string, input io.Reader, output io.Writer) error {
	if c.run != nil {
		return c.run(ctx, value, args, input, output)
	}
	grant, rel, err := Parse(value)
	if err != nil {
		return err
	}
	access, err := c.Broker(ctx, grant)
	if err != nil {
		return err
	}
	provider := ""
	switch access.Scope {
	case "https://www.googleapis.com/auth/drive":
		provider = "drive"
	case "account_info.read files.metadata.read files.content.read files.content.write":
		provider = "dropbox"
	default:
		return errors.New("cloud permission does not allow file management")
	}
	token, _ := json.Marshal(map[string]any{"access_token": access.AccessToken, "token_type": "Bearer", "expiry": access.ExpiresAt})
	config := []byte("[cloud]\ntype = " + provider + "\ntoken = " + string(token) + "\n")
	file, err := configFile(config)
	if err != nil {
		return err
	}
	defer file.Close()
	binary := c.Binary
	if binary == "" {
		binary = "/usr/bin/rclone"
	}
	argv := append([]string{}, args...)
	argv = append(argv, "cloud:"+rel, "--config", "/proc/self/fd/3", "--retries", "1", "--low-level-retries", "2", "--contimeout", "15s", "--timeout", "2m", "--buffer-size", "1M")
	if provider == "drive" {
		argv = append(argv, "--drive-skip-shortcuts")
	}
	cmd := exec.CommandContext(ctx, binary, argv...)
	cmd.ExtraFiles = []*os.File{file}
	cmd.Stdin = input
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	err = cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("cloud operation failed; check connection, permission and available space")
	}
	return nil
}

func configFile(config []byte) (*os.File, error) {
	fd, err := unix.MemfdCreate("panasms-cloud-config", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "cloud-config")
	if _, err = file.Write(config); err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err == nil {
		_, err = unix.FcntlInt(file.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL)
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

type bounded struct {
	bytes.Buffer
	limit int
}

func (b *bounded) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("cloud response is too large")
	}
	return b.Buffer.Write(p)
}
func (c *Client) List(ctx context.Context, value string) ([]localfs.Entry, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	out := &bounded{limit: 8 << 20}
	if err := c.command(ctx, value, []string{"lsjson", "--hash"}, nil, out); err != nil {
		return nil, err
	}
	var rows []struct {
		Name, ID string
		Size     int64
		IsDir    bool
		ModTime  time.Time
		Hashes   map[string]string
	}
	if json.Unmarshal(out.Bytes(), &rows) != nil {
		return nil, errors.New("invalid cloud listing")
	}
	if len(rows) > 10000 {
		return nil, errors.New("too many cloud items; open a subdirectory")
	}
	result := []localfs.Entry{}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Name == "" || r.Name == "." || r.Name == ".." || strings.ContainsAny(r.Name, "/\x00") {
			return nil, errors.New("cloud contains a name unsupported by this file manager")
		}
		if seen[r.Name] {
			return nil, errors.New("cloud folder contains duplicate names; rename them in the provider before using this folder")
		}
		seen[r.Name] = true
		raw, _ := json.Marshal(r)
		hash := sha256.Sum256(raw)
		result = append(result, localfs.Entry{Name: r.Name, Path: Join(value, r.Name), Directory: r.IsDir, Size: r.Size, Modified: float64(r.ModTime.Unix()), Revision: hex.EncodeToString(hash[:])})
	}
	return result, nil
}
func (c *Client) Stat(ctx context.Context, value string) (localfs.Entry, error) {
	_, rel, err := Parse(value)
	if err != nil {
		return localfs.Entry{}, err
	}
	if rel == "" {
		return localfs.Entry{Name: "Cloud", Path: value, Directory: true, Revision: "root"}, nil
	}
	entries, err := c.List(ctx, Parent(value))
	if err != nil {
		return localfs.Entry{}, err
	}
	for _, entry := range entries {
		if entry.Name == path.Base(rel) {
			return entry, nil
		}
	}
	return localfs.Entry{}, os.ErrNotExist
}
func (c *Client) Read(ctx context.Context, value string, out io.Writer) error {
	return c.command(ctx, value, []string{"cat"}, nil, out)
}
func (c *Client) Write(ctx context.Context, value string, in io.Reader, size int64) error {
	if size < 0 {
		return errors.New("cloud document must be exported before copying")
	}
	return c.command(ctx, value, []string{"rcat", "--size", fmt.Sprint(size)}, in, io.Discard)
}
func (c *Client) Mkdir(ctx context.Context, value string) error {
	return c.command(ctx, value, []string{"mkdir"}, nil, io.Discard)
}
func (c *Client) Delete(ctx context.Context, value string, directory bool) error {
	_, rel, err := Parse(value)
	if err != nil || rel == "" {
		return errors.New("cannot remove an account root")
	}
	command := "deletefile"
	if directory {
		command = "rmdir"
	}
	return c.command(ctx, value, []string{command}, nil, io.Discard)
}
func (c *Client) Publish(ctx context.Context, stage, destination string) error {
	sg, sr, err := Parse(stage)
	if err != nil {
		return err
	}
	dg, _, err := Parse(destination)
	if err != nil || sg != dg {
		return errors.New("invalid cloud publish")
	}
	// command appends the final argument; both endpoints use the same grant.
	return c.command(ctx, destination, []string{"moveto", "cloud:" + sr}, nil, io.Discard)
}

func (c *Client) Upload(ctx context.Context, value string, in io.Reader, size int64, expected string) error {
	if size < 0 {
		return errors.New("upload size is required")
	}
	previous, err := c.Stat(ctx, value)
	if expected == "" {
		if !os.IsNotExist(err) {
			return errors.New("destination already exists or is unavailable")
		}
	} else if err != nil || previous.Directory || previous.Revision != expected {
		return errors.New("destination changed; choose what to do again")
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	stage := Join(Parent(value), ".panasms-upload-"+hex.EncodeToString(random[:]))
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Delete(cleanup, stage, false)
	}()
	checksum := sha256.New()
	if err = c.Write(ctx, stage, io.TeeReader(in, checksum), size); err != nil {
		return err
	}
	check := sha256.New()
	if err = c.Read(ctx, stage, check); err != nil {
		return err
	}
	if !bytes.Equal(check.Sum(nil), checksum.Sum(nil)) {
		return errors.New("upload verification failed; previous file preserved")
	}
	current, err := c.Stat(ctx, value)
	if expected == "" {
		if !os.IsNotExist(err) {
			return errors.New("destination changed; previous file preserved")
		}
	} else if err != nil || current.Revision != expected {
		return errors.New("destination changed; previous file preserved")
	}
	return c.Publish(ctx, stage, value)
}
