package cloudfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf16"
)

const driveScope = "https://www.googleapis.com/auth/drive"

type ArchiveRevision struct{ scope, id, token, mime string }

var ErrArchiveUnconfirmed = errors.New("Could not confirm the cloud archive update. Refresh the folder.")
var errVersion = errors.New("destination changed; choose what to do again")
var errCloudRevision = errors.New("Cloud archive revision is unavailable")

// Capture the provider's write precondition before reading the archive bytes.
func (c *Client) ArchiveRevision(ctx context.Context, value, id string) (*ArchiveRevision, error) {
	grant, rel, err := Parse(value)
	if err != nil || rel == "" {
		return nil, errCloudRevision
	}
	access, err := c.Broker(ctx, grant)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var req *http.Request
	switch access.Scope {
	case driveScope:
		if id == "" {
			return nil, errCloudRevision
		}
		req, err = http.NewRequestWithContext(ctx, "GET", "https://www.googleapis.com/drive/v2/files/"+url.PathEscape(id)+"?fields=id,etag,mimeType&supportsAllDrives=true", nil)
	case dropboxScope:
		body, _ := json.Marshal(map[string]string{"path": "/" + rel})
		req, err = http.NewRequestWithContext(ctx, "POST", "https://api.dropboxapi.com/2/files/get_metadata", bytes.NewReader(body))
	default:
		return nil, errCloudRevision
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+access.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := archiveHTTP.Do(req)
	if err != nil {
		return nil, errCloudRevision
	}
	defer resp.Body.Close()
	var meta struct{ ID, Etag, MimeType, Rev string }
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&meta) != nil {
		return nil, errCloudRevision
	}
	version := &ArchiveRevision{scope: access.Scope, id: meta.ID, mime: meta.MimeType, token: meta.Rev}
	if access.Scope == driveScope {
		version.token = meta.Etag
		if meta.ID != id || len(meta.Etag) < 2 || meta.Etag[0] != '"' || meta.Etag[len(meta.Etag)-1] != '"' {
			return nil, errCloudRevision
		}
	}
	if version.token == "" || version.id == "" {
		return nil, errCloudRevision
	}
	return version, nil
}

var archiveHTTP = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (c *Client) ReplaceArchive(ctx context.Context, value, expected string, version *ArchiveRevision, data *os.File, size int64) error {
	if version == nil {
		return errCloudRevision
	}
	item, err := c.Stat(ctx, value)
	if err != nil {
		return err
	}
	if item.Revision != expected {
		return errVersion
	}
	grant, rel, err := Parse(value)
	if err != nil {
		return err
	}
	access, err := c.Broker(ctx, grant)
	if err != nil {
		return err
	}
	if access.Scope != version.scope {
		return errCloudRevision
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	reader := io.NewSectionReader(data, 0, size)
	var req *http.Request
	switch version.scope {
	case driveScope:
		if item.ProviderID != version.id {
			return errVersion
		}
		req, err = http.NewRequestWithContext(ctx, "PUT", "https://www.googleapis.com/upload/drive/v2/files/"+url.PathEscape(version.id)+"?uploadType=media&supportsAllDrives=true&fields=id,etag", reader)
		if err != nil {
			return err
		}
		req.Header.Set("If-Match", version.token)
		req.Header.Set("Content-Type", version.mime)
	case dropboxScope:
		req, err = http.NewRequestWithContext(ctx, "POST", "https://content.dropboxapi.com/2/files/upload", reader)
		if err != nil {
			return err
		}
		args, _ := json.Marshal(map[string]any{"path": "/" + rel, "mode": map[string]string{".tag": "update", "update": version.token}, "autorename": false, "strict_conflict": true})
		req.Header.Set("Dropbox-API-Arg", asciiHeader(string(args)))
		req.Header.Set("Content-Type", "application/octet-stream")
	default:
		return errCloudRevision
	}
	req.ContentLength = size
	req.Header.Set("Authorization", "Bearer "+access.AccessToken)
	resp, err := archiveHTTP.Do(req)
	if err != nil {
		return ErrArchiveUnconfirmed
	}
	defer resp.Body.Close()
	if resp.StatusCode == 409 || resp.StatusCode == 412 {
		return errVersion
	}
	if resp.StatusCode >= 500 {
		return ErrArchiveUnconfirmed
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("Cloud archive update was rejected")
	}
	return nil
}

// Dropbox's argument header accepts ASCII JSON, including escaped surrogate pairs.
func asciiHeader(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r >= 32 && r < 127 {
			out.WriteRune(r)
		} else {
			for _, u := range utf16.Encode([]rune{r}) {
				fmt.Fprintf(&out, "\\u%04x", u)
			}
		}
	}
	return out.String()
}
