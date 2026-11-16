package agentuse

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type ServerUpgrade struct {
	ID              string `json:"id"`
	FromVersion     string `json:"from_version"`
	TargetVersion   string `json:"target_version"`
	Status          string `json:"status" enum:"checking,downloading,prepared,installing,restarting,succeeded,failed,unchanged"`
	ReleaseURL      string `json:"release_url"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	TotalBytes      int64  `json:"total_bytes"`
	Error           string `json:"error"`
	RequestedBy     string `json:"requested_by"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	CompletedAt     *int64 `json:"completed_at"`
}

type ServerUpgradeState struct {
	CurrentVersion string          `json:"current_version"`
	Platform       string          `json:"platform"`
	Supported      bool            `json:"supported"`
	Reason         string          `json:"reason"`
	Active         *ServerUpgrade  `json:"active"`
	Upgrades       []ServerUpgrade `json:"upgrades" nullable:"false"`
	NextCursor     *string         `json:"next_cursor"`
}

type ServerRelease struct {
	Version        string `json:"version"`
	CurrentVersion string `json:"current_version"`
	Available      bool   `json:"available"`
	URL            string `json:"url"`
	Notes          string `json:"notes"`
	PublishedAt    string `json:"published_at"`
}

func (c *Client) ServerUpgrades(ctx context.Context, cursor string, limit int) (*ServerUpgradeState, error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/api/upgrades"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out ServerUpgradeState
	err := c.do(ctx, "", http.MethodGet, path, nil, &out)
	return &out, err
}
func (c *Client) LatestServerRelease(ctx context.Context) (*ServerRelease, error) {
	var out ServerRelease
	err := c.do(ctx, "", http.MethodGet, "/api/upgrades/latest", nil, &out)
	return &out, err
}
func (c *Client) StartServerUpgrade(ctx context.Context) (*ServerUpgrade, error) {
	var out ServerUpgrade
	err := c.do(ctx, "", http.MethodPost, "/api/upgrades", nil, &out)
	return &out, err
}
func (c *Client) GetServerUpgrade(ctx context.Context, id string) (*ServerUpgrade, error) {
	var out ServerUpgrade
	err := c.do(ctx, "", http.MethodGet, "/api/upgrades/"+esc(id), nil, &out)
	return &out, err
}
