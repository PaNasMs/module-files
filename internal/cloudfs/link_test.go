package cloudfs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PaNasMs/module-sdk/external"
)

const grant = "0123456789abcdef0123456789abcdef"

func TestDirectLinkAsksDropboxForATemporaryLink(t *testing.T) {
	reply := `{"metadata":{".tag":"file"},"link":"https://uc123.dl.dropboxusercontent.com/cd/0/get/abc/file"}`
	var path, auth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		path, auth = r.URL.Path+" "+body["path"], r.Header.Get("Authorization")
		_, _ = w.Write([]byte(reply))
	}))
	defer api.Close()
	c := &Client{DropboxAPI: api.URL, Broker: func(context.Context, string) (external.Access, error) {
		return external.Access{AccessToken: "token", Scope: dropboxScope}, nil
	}}
	link, err := c.DirectLink(context.Background(), "cloud:"+grant+"/Folder/файл 1.bin")
	if err != nil || link != "https://uc123.dl.dropboxusercontent.com/cd/0/get/abc/file" {
		t.Fatalf("link %q, error %v", link, err)
	}
	if path != "/2/files/get_temporary_link /Folder/файл 1.bin" || auth != "Bearer token" {
		t.Fatalf("request %q %q", path, auth)
	}
	// A reply pointing anywhere else is not passed on to the browser.
	reply = `{"link":"https://evil.example/file"}`
	if _, err = c.DirectLink(context.Background(), "cloud:"+grant+"/a.bin"); err == nil {
		t.Fatal("foreign link accepted")
	}
	if _, err = c.DirectLink(context.Background(), "cloud:"+grant); err == nil {
		t.Fatal("account root accepted")
	}
}

func TestDirectLinkIsDropboxOnly(t *testing.T) {
	c := &Client{DropboxAPI: "http://127.0.0.1:1", Broker: func(context.Context, string) (external.Access, error) {
		return external.Access{AccessToken: "token", Scope: "https://www.googleapis.com/auth/drive"}, nil
	}}
	if _, err := c.DirectLink(context.Background(), "cloud:"+grant+"/a.bin"); !errors.Is(err, ErrNoDirectLink) {
		t.Fatalf("error %v", err)
	}
}

func TestDirectLinkHost(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://uc123.dl.dropboxusercontent.com/cd/0/get/x": true,
		"https://dl.dropboxusercontent.com/x":                true,
		"http://uc123.dl.dropboxusercontent.com/x":           false,
		"https://dl.dropboxusercontent.com.evil.example/x":   false,
		"https://user@dl.dropboxusercontent.com/x":           false,
		"https://dl.dropboxusercontent.com:8443/x":           false,
		"https://www.dropbox.com/s/x":                        false,
		"javascript:alert(1)":                                false,
	} {
		if DirectLinkHost(raw) != want {
			t.Errorf("%s: want %v", raw, want)
		}
	}
}
