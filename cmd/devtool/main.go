package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		panic("usage: devtool check|package")
	}
	var err error
	switch os.Args[1] {
	case "check":
		err = check()
	case "package":
		err = pack()
	default:
		err = fmt.Errorf("unknown command")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func readJSON(path string) (map[string]json.RawMessage, error) {
	raw, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	var d map[string]json.RawMessage
	e = json.Unmarshal(raw, &d)
	return d, e
}
func check() error {
	en, e := readJSON("frontend/locales/en.json")
	if e != nil {
		return e
	}
	for _, lang := range []string{"ru", "uk"} {
		d, e := readJSON("frontend/locales/" + lang + ".json")
		if e != nil {
			return e
		}
		if len(d) != len(en) {
			return fmt.Errorf("translation keys differ: %s", lang)
		}
		for key := range en {
			if _, ok := d[key]; !ok {
				return fmt.Errorf("missing translation %s: %s", lang, key)
			}
		}
	}
	fmt.Println("Translation keys checked")
	return nil
}
func pack() error {
	m, e := readJSON("manifest.json")
	if e != nil {
		return e
	}
	var id, version string
	json.Unmarshal(m["id"], &id)
	json.Unmarshal(m["version"], &version)
	archBytes, e := exec.Command("go", "env", "GOARCH").Output()
	if e != nil {
		return e
	}
	arch := strings.TrimSpace(string(archBytes))
	machine := map[string]uint16{"arm64": 183, "amd64": 62}[arch]
	if machine == 0 {
		return fmt.Errorf("unsupported architecture")
	}
	b, e := os.ReadFile("dist/bin/server")
	if e != nil {
		return e
	}
	if len(b) < 20 || string(b[:6]) != "\x7fELF\x02\x01" || binary.LittleEndian.Uint16(b[18:20]) != machine {
		return fmt.Errorf("binary architecture mismatch")
	}
	if os.Getenv("GITHUB_REF_TYPE") == "tag" && os.Getenv("GITHUB_REF_NAME") != "v"+version {
		return fmt.Errorf("tag does not match version")
	}
	payload := map[string][]byte{}
	for _, folder := range []string{"bin", "ui"} {
		e = filepath.WalkDir("dist/"+folder, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in payload")
			}
			b, err := os.ReadFile(path)
			payload[strings.TrimPrefix(filepath.ToSlash(path), "dist/")] = b
			return err
		})
		if e != nil {
			return e
		}
	}
	for _, name := range []string{"LICENSE", "NOTICE"} {
		b, e := os.ReadFile(name)
		if e != nil {
			return e
		}
		payload[name] = b
		if e = os.WriteFile("dist/"+name, b, 0644); e != nil {
			return e
		}
	}
	hashes := map[string]string{}
	names := []string{}
	for name, b := range payload {
		sum := sha256.Sum256(b)
		hashes[name] = hex.EncodeToString(sum[:])
		names = append(names, name)
	}
	sort.Strings(names)
	m["architecture"], _ = json.Marshal(arch)
	m["files"], _ = json.Marshal(hashes)
	manifest, e := json.Marshal(m)
	if e != nil {
		return e
	}
	f, e := os.Create("dist/" + id + "-" + version + "-" + arch + ".unsigned.zip")
	if e != nil {
		return e
	}
	defer f.Close()
	z := zip.NewWriter(f)
	write := func(name string, data []byte) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0644)
		if strings.HasPrefix(name, "bin/") {
			h.SetMode(0755)
		}
		w, e := z.CreateHeader(h)
		if e != nil {
			return e
		}
		_, e = w.Write(data)
		return e
	}
	if e = write("manifest.json", manifest); e != nil {
		return e
	}
	for _, name := range names {
		if e = write(name, payload[name]); e != nil {
			return e
		}
	}
	return z.Close()
}
