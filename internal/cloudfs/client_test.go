package cloudfs

import (
	"io"
	"strings"
	"testing"
)

func TestLocationValidation(t *testing.T) {
	grant := strings.Repeat("a", 32)
	for _, p := range []string{"cloud:" + grant, "cloud:" + grant + "/folder/file.txt"} {
		if _, _, e := Parse(p); e != nil {
			t.Fatal(e)
		}
	}
	for _, p := range []string{"/local", "cloud:bad", "cloud:" + grant + "/../secret", "cloud:" + grant + "//absolute", "cloud:" + grant + "/a/../b", "cloud:" + grant + "/a\x00b"} {
		if _, _, e := Parse(p); e == nil {
			t.Fatalf("accepted %q", p)
		}
	}
}
func TestCloudParent(t *testing.T) {
	g := "cloud:" + strings.Repeat("b", 32)
	if Parent(g+"/folder") != g || Parent(g+"/folder/file") != g+"/folder" {
		t.Fatal("invalid parent")
	}
}

func TestConfigurationIsSeekableAndSealed(t *testing.T) {
	file, err := configFile([]byte("[cloud]\ntype = drive\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for range 2 {
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(file)
		if err != nil || string(data) != "[cloud]\ntype = drive\n" {
			t.Fatal("configuration must support rereading")
		}
	}
	if _, err = file.Write([]byte("modified")); err == nil {
		t.Fatal("configuration must be immutable")
	}
}
