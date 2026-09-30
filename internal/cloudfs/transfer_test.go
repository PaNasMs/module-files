package cloudfs

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/PaNasMs/module-files/internal/localfs"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

type object struct {
	data string
	dir  bool
}

func mockCloud(objects map[string]object) *Client {
	return &Client{run: func(ctx context.Context, value string, args []string, in io.Reader, out io.Writer) error {
		switch args[0] {
		case "lsjson":
			if item, ok := objects[value]; !ok || !item.dir {
				return os.ErrNotExist
			}
			rows := []map[string]any{}
			for p, o := range objects {
				if p != value && Parent(p) == value {
					_, rel, _ := Parse(p)
					parts := strings.Split(rel, "/")
					rows = append(rows, map[string]any{"Name": parts[len(parts)-1], "ID": p + o.data, "Size": len(o.data), "IsDir": o.dir, "ModTime": time.Unix(100, 0).UTC()})
				}
			}
			return json.NewEncoder(out).Encode(rows)
		case "cat":
			o, ok := objects[value]
			if !ok {
				return os.ErrNotExist
			}
			_, err := io.WriteString(out, o.data)
			return err
		case "rcat":
			data, err := io.ReadAll(in)
			if err != nil {
				return err
			}
			objects[value] = object{data: string(data)}
			return nil
		case "moveto":
			grant, _, _ := Parse(value)
			source := "cloud:" + grant + "/" + strings.TrimPrefix(args[1], "cloud:")
			o, ok := objects[source]
			if !ok {
				return os.ErrNotExist
			}
			objects[value] = o
			delete(objects, source)
			return nil
		case "mkdir":
			objects[value] = object{dir: true}
			return nil
		case "deletefile", "rmdir":
			delete(objects, value)
			return nil
		}
		return errors.New("unexpected command")
	}}
}
func TestCrossAccountMergePolicies(t *testing.T) {
	for _, policy := range []localfs.Conflict{localfs.Skip, localfs.Rename, localfs.Replace} {
		for _, move := range []bool{false, true} {
			name := string(policy)
			if move {
				name += "-move"
			}
			t.Run(name, func(t *testing.T) {
				a := "cloud:" + strings.Repeat("a", 32)
				b := "cloud:" + strings.Repeat("b", 32)
				objects := map[string]object{a: {dir: true}, a + "/folder": {dir: true}, a + "/folder/common": {data: "new"}, a + "/folder/unique": {data: "new unique"}, b: {dir: true}, b + "/folder": {dir: true}, b + "/folder/common": {data: "old"}, b + "/folder/keep": {data: "keep"}}
				transfer := Transfer{Cloud: mockCloud(objects), Local: &localfs.Engine{}}
				if _, err := transfer.Run(context.Background(), a+"/folder", b+"/folder", move, policy, ""); err != nil {
					t.Fatal(err)
				}
				if objects[b+"/folder/keep"].data != "keep" || objects[b+"/folder/unique"].data != "new unique" {
					t.Fatal("lost destination or new file")
				}
				want := "old"
				if policy == localfs.Replace {
					want = "new"
				}
				if objects[b+"/folder/common"].data != want {
					t.Fatal("conflict policy ignored")
				}
				if policy == localfs.Rename && objects[b+"/folder/common (1)"].data != "new" {
					t.Fatal("rename failed")
				}
				if move && policy == localfs.Skip && objects[a+"/folder/common"].data != "new" {
					t.Fatal("skipped source removed")
				}
			})
		}
	}
}
func TestCloudToLocalMove(t *testing.T) {
	a := "cloud:" + strings.Repeat("a", 32)
	objects := map[string]object{a: {dir: true}, a + "/file": {data: "payload"}}
	dest := t.TempDir() + "/file"
	engine := Transfer{Cloud: mockCloud(objects), Local: &localfs.Engine{}}
	if _, err := engine.Run(context.Background(), a+"/file", dest, true, localfs.Skip, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "payload" {
		t.Fatal(string(data), err)
	}
	if _, ok := objects[a+"/file"]; ok {
		t.Fatal("source retained after successful move")
	}
}
func TestFailedVerificationKeepsSource(t *testing.T) {
	a := "cloud:" + strings.Repeat("a", 32)
	b := "cloud:" + strings.Repeat("b", 32)
	objects := map[string]object{a: {dir: true}, a + "/file": {data: "payload"}, b: {dir: true}}
	cloud := mockCloud(objects)
	run := cloud.run
	cloud.run = func(ctx context.Context, v string, args []string, in io.Reader, out io.Writer) error {
		if args[0] == "cat" && strings.Contains(v, ".panasms-transfer-") {
			_, err := io.WriteString(out, "corruption")
			return err
		}
		return run(ctx, v, args, in, out)
	}
	engine := Transfer{Cloud: cloud, Local: &localfs.Engine{}}
	if _, err := engine.Run(context.Background(), a+"/file", b+"/file", true, localfs.Skip, ""); err == nil {
		t.Fatal("accepted corrupt copy")
	}
	if objects[a+"/file"].data != "payload" {
		t.Fatal("source lost")
	}
	if _, ok := objects[b+"/file"]; ok {
		t.Fatal("corrupt file published")
	}
}

func TestTransferReportsProgressIncludingVerificationAndSkippedFiles(t *testing.T) {
	root := "cloud:" + strings.Repeat("a", 64)
	objects := map[string]object{root: {dir: true}, root + "/source": {dir: true}, root + "/source/empty": {}, root + "/source/data": {data: strings.Repeat("x", 65536)}, root + "/dest": {dir: true}, root + "/dest/empty": {}}
	var values []float64
	transfer := &Transfer{Cloud: mockCloud(objects), Local: &localfs.Engine{}, Progress: func(p float64) { values = append(values, p) }}
	result, err := transfer.Run(context.Background(), root+"/source", root+"/dest", false, localfs.Skip, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Copied != 1 || result.Skipped != 1 {
		t.Fatal(result)
	}
	if len(values) < 2 || values[0] != 0 || values[len(values)-1] != 99 {
		t.Fatal(values)
	}
	for i, p := range values {
		if p < 0 || p >= 100 || i > 0 && p < values[i-1] {
			t.Fatal(values)
		}
	}
	if transfer.completed != transfer.total {
		t.Fatalf("completed=%d total=%d", transfer.completed, transfer.total)
	}
}
