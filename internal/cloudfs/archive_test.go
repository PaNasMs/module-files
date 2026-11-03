package cloudfs

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/PaNasMs/module-sdk/external"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type archiveTransport func(*http.Request) (*http.Response, error)

func (f archiveTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderChecksArchiveVersionAtWrite(t *testing.T) {
	for _, scope := range []string{driveScope, dropboxScope} {
		t.Run(scope, func(t *testing.T) {
			ctx := context.Background()
			value := "cloud:" + strings.Repeat("a", 32) + "/Фото🎞.zip"
			c := mockCloud(map[string]object{value: {data: "old"}, "cloud:" + strings.Repeat("a", 32): {dir: true}})
			c.Broker = func(context.Context, string) (external.Access, error) {
				return external.Access{AccessToken: "test", Scope: scope}, nil
			}
			before, e := c.Stat(ctx, value)
			if e != nil {
				t.Fatal(e)
			}
			providerVersion := "one"
			stored := "old"
			prior := archiveHTTP
			defer func() { archiveHTTP = prior }()
			archiveHTTP = &http.Client{Transport: archiveTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer test" {
					t.Fatal("missing grant")
				}
				result := 200
				body := "{}"
				switch {
				case r.Method == "GET" || r.URL.Path == "/2/files/get_metadata":
					data, _ := json.Marshal(map[string]string{"id": before.ProviderID, "etag": "\"" + providerVersion + "\"", "rev": providerVersion, "mimeType": "application/zip"})
					body = string(data)
				case r.Method == "PUT":
					if r.Header.Get("If-Match") != "\""+providerVersion+"\"" {
						result = 412
					}
				case r.URL.Path == "/2/files/upload":
					var arg struct {
						Path   string
						Mode   map[string]string
						Strict bool `json:"strict_conflict"`
					}
					if err := json.Unmarshal([]byte(r.Header.Get("Dropbox-API-Arg")), &arg); err != nil || arg.Path != "/Фото🎞.zip" || arg.Mode[".tag"] != "update" || !arg.Strict {
						t.Fatal("incorrect conditional upload", err, arg)
					}
					if arg.Mode["update"] != providerVersion {
						result = 409
					}
				default:
					t.Fatal("unexpected provider request")
				}
				if result == 200 && (r.Method == "PUT" || r.URL.Path == "/2/files/upload") {
					raw, _ := io.ReadAll(r.Body)
					stored = string(raw)
				}
				return &http.Response{StatusCode: result, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
			})}
			version, e := c.ArchiveRevision(ctx, value, before.ProviderID)
			if e != nil {
				t.Fatal(e)
			}
			f, e := os.CreateTemp(t.TempDir(), "new")
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			f.WriteString("new")
			providerVersion = "two"
			if err := c.ReplaceArchive(ctx, value, before.Revision, version, f, 3); !errors.Is(err, errVersion) || stored != "old" {
				t.Fatalf("stale write stored %q: %v", stored, err)
			}
			version, e = c.ArchiveRevision(ctx, value, before.ProviderID)
			if e != nil {
				t.Fatal(e)
			}
			if err := c.ReplaceArchive(ctx, value, before.Revision, version, f, 3); err != nil || stored != "new" {
				t.Fatalf("current write stored %q: %v", stored, err)
			}
		})
	}
}
