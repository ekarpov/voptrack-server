package acquisitionclient

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

type Page struct {
	Results []Operation `json:"results"`
	Next    string      `json:"next_cursor"`
}

func (c *Client) Catalog(ctx context.Context, token, cursor string) (Page, error) {
	var page Page
	p := "/admin/api/v1/backup-operations?limit=50"
	if len(cursor) > 8192 {
		return page, ErrInvalid
	}
	if cursor != "" {
		p += "&cursor=" + url.QueryEscape(cursor)
	}
	e := c.json(ctx, "GET", p, token, nil, 200, &page)
	if e != nil {
		return Page{}, e
	}
	if len(page.Results) > 50 || len(page.Next) > 8192 {
		return Page{}, ErrInvalid
	}
	seen := map[string]bool{}
	for _, op := range page.Results {
		if seen[op.ID] || op.State != "complete" || validate(op, op.ID, op.Kind) != nil || !ValidID(op.ID) || (op.Kind != "backup" && op.Kind != "evidence") {
			return Page{}, ErrInvalid
		}
		seen[op.ID] = true
	}
	return page, nil
}

type Credential struct {
	ID      string    `json:"id"`
	Token   string    `json:"token"`
	Created time.Time `json:"created_at"`
	Expires time.Time `json:"expires_at"`
}

func (c *Client) Provision(ctx context.Context, id, admin string, seconds int) (Credential, error) {
	var v Credential
	if !artifactID.MatchString(id) || seconds < 60 || seconds > 31536000 {
		return v, ErrInvalid
	}
	raw, _ := json.Marshal(map[string]int{"expires_in_seconds": seconds})
	e := c.json(ctx, "PUT", "/admin/api/v1/backup-collectors/"+id, admin, strings.NewReader(string(raw)), 201, &v)
	if e == nil && (v.ID != id || v.Token == "" || v.Expires.IsZero()) {
		e = ErrInvalid
	}
	return v, e
}
func (c *Client) Revoke(ctx context.Context, id, admin string) error {
	if !artifactID.MatchString(id) {
		return ErrInvalid
	}
	resp, e := c.request(ctx, "DELETE", "/admin/api/v1/backup-collectors/"+id, admin, "", nil)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return refused(resp)
	}
	return nil
}

// Validate checks only the supported descriptor contract. MAC verification is
// intentionally separate and requires the independently held recovery key.
func Validate(op Operation) error {
	if !ValidID(op.ID) || (op.Kind != "backup" && op.Kind != "evidence") {
		return ErrInvalid
	}
	return validate(op, op.ID, op.Kind)
}
