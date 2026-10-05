package cloudfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const dropboxScope = "account_info.read files.metadata.read files.content.read files.content.write"

// ErrNoDirectLink means the provider offers no short-lived download link; the caller streams the file instead.
var ErrNoDirectLink = errors.New("direct download is not available for this cloud")

// DirectLinkHost reports whether a URL is a Dropbox content link of the kind DirectLink returns.
func DirectLinkHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "dl.dropboxusercontent.com" || strings.HasSuffix(host, ".dl.dropboxusercontent.com")
}

// DirectLink returns a temporary link (valid for about four hours) that lets the browser fetch a
// Dropbox file straight from the provider. It does not make the file public or create a shared link.
func (c *Client) DirectLink(ctx context.Context, value string) (string, error) {
	grant, rel, err := Parse(value)
	if err != nil || rel == "" {
		return "", errors.New("invalid cloud location")
	}
	access, err := c.Broker(ctx, grant)
	if err != nil {
		return "", err
	}
	if access.Scope != dropboxScope {
		return "", ErrNoDirectLink
	}
	body, _ := json.Marshal(map[string]string{"path": "/" + rel})
	endpoint := c.DropboxAPI
	if endpoint == "" {
		endpoint = "https://api.dropboxapi.com"
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/2/files/get_temporary_link", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+access.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", errors.New("cloud operation failed; check connection, permission and available space")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != 200 {
		return "", errors.New("cloud file cannot be downloaded")
	}
	var reply struct {
		Link string `json:"link"`
	}
	if json.Unmarshal(raw, &reply) != nil || !DirectLinkHost(reply.Link) {
		return "", errors.New("cloud file cannot be downloaded")
	}
	return reply.Link, nil
}
