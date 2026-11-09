package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

type objectReference struct {
	Dataset string `json:"dataset_id"`
	Type    string `json:"meta_id"`
	ID      string `json:"object_id"`
}
type contentReference struct {
	Store string `json:"storeID"`
	Item  string `json:"itemID"`
}
type attachmentView struct {
	Key, Name, MIME, Size, URL, PreviewURL string
	Image                                  bool
	content                                contentReference
}

func directContent(value string) (contentReference, bool) {
	store, item, ok := strings.Cut(value, "//")
	if !ok || store == "" || item == "" || strings.ContainsAny(store+item, "\r\n\x00") {
		return contentReference{}, false
	}
	return contentReference{store, item}, true
}
func mediaReference(value string) (objectReference, bool) {
	var parts []string
	if json.Unmarshal([]byte(value), &parts) != nil || len(parts) != 3 || !strings.HasPrefix(parts[1], "com.voptrack.media") {
		return objectReference{}, false
	}
	return objectReference{parts[0], parts[1], parts[2]}, parts[0] != "" && parts[2] != ""
}
func blobContent(v any) (contentReference, bool) {
	text, ok := v.(string)
	if !ok {
		return contentReference{}, false
	}
	raw, e := base64.StdEncoding.DecodeString(text)
	if e != nil {
		return contentReference{}, false
	}
	var ref contentReference
	if json.Unmarshal(raw, &ref) != nil {
		return ref, false
	}
	return ref, ref.Store != "" && ref.Item != "" && !strings.ContainsAny(ref.Store+ref.Item, "\r\n\x00")
}
func imageMIME(kind string) bool {
	switch kind {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}
func attachmentKey(ref contentReference) string {
	sum := sha256.Sum256([]byte(ref.Store + "\x00" + ref.Item))
	return hex.EncodeToString(sum[:16])
}
func (a *app) recordAttachments(ctx context.Context, scope string, src Source, rec readRecord) ([]attachmentView, bool) {
	token, e := src.Token(ctx)
	if e != nil {
		return nil, true
	}
	var out []attachmentView
	unavailable := false
	seen := map[string]bool{}
	add := func(ref contentReference, name, kind string) {
		key := attachmentKey(ref)
		if seen[key] {
			return
		}
		seen[key] = true
		if name == "" {
			name = "Attachment"
		}
		q := url.Values{"scope": {scope}, "dataset": {rec.Dataset}, "type": {rec.Type}, "id": {rec.ID}, "attachment": {key}}
		view := attachmentView{Key: key, Name: name, MIME: kind, URL: "/attachment?" + q.Encode(), content: ref, Image: imageMIME(kind)}
		if view.Image {
			q.Set("preview", "1")
			view.PreviewURL = "/attachment?" + q.Encode()
		}
		out = append(out, view)
	}
	loadMedia := func(ref objectReference) {
		// The record's reader is not replaced by an owner's/private credential. A
		// cross-store metadata reference must be readable with this same authority.
		media, e := a.tessera.recordRead(ctx, token, ref.Dataset, ref.Type, ref.ID)
		if e != nil {
			unavailable = true
			return
		}
		if state, ok := media.Values["state"].(string); !ok || state != "ready" {
			unavailable = true
			return
		}
		content, ok := blobContent(media.Values["contentReference"])
		if !ok {
			unavailable = true
			return
		}
		name := displayBlob(media.Values["name"])
		kind, _ := media.Values["contentType"].(string)
		add(content, name, kind)
	}
	for _, f := range rec.Fields {
		if f.Type != "media" {
			continue
		}
		text, ok := rec.Values[f.ID].(string)
		if !ok || text == "" {
			continue
		}
		if content, ok := directContent(text); ok {
			var info struct {
				Type string `json:"content_type"`
			}
			e := a.tessera.call(ctx, "GET", "/v1/stores/"+url.PathEscape(content.Store)+"/items/"+url.PathEscape(content.Item)+"?representation=original", token, "", nil, 200, &info)
			if e != nil {
				unavailable = true
				continue
			}
			add(content, localizeName(f.Name, translator("en")), info.Type)
		} else if ref, ok := mediaReference(text); ok {
			loadMedia(ref)
		} else {
			unavailable = true
		}
	}
	metas, e := a.tessera.types(ctx, token, src.Dataset)
	if e != nil {
		return out, true
	}
	hasRelations := false
	for _, m := range metas {
		if m.ID == "com.voptrack.media_attachment" {
			hasRelations = true
		}
	}
	if hasRelations {
		subject := objectReference{rec.Dataset, rec.Type, rec.ID}
		query := map[string]any{"type_scope": map[string]any{"kind": "exact"}, "filter": map[string]any{"field": map[string]any{"id": "subject", "exact": map[string]any{"type": "reference", "value": subject}}}, "result_sort": []any{map[string]any{"field_id": "displayOrder", "ascending": true}}}
		relationships, cut, e := a.tessera.recordRows(ctx, token, src.Dataset, "com.voptrack.media_attachment", query, 32)
		if e != nil || cut {
			unavailable = true
		} else {
			for _, link := range relationships {
				raw, _ := json.Marshal(link.Values["subject"])
				var got objectReference
				if json.Unmarshal(raw, &got) != nil || got != subject {
					unavailable = true
					continue
				}
				raw, _ = json.Marshal(link.Values["media"])
				var media objectReference
				if json.Unmarshal(raw, &media) != nil || media.ID == "" {
					unavailable = true
					continue
				}
				loadMedia(media)
			}
		}
	}
	return out, unavailable
}

// Resolved URLs are credentials. They stay within the Console, are never put
// into a redirect/HTML/log, and are fetched with redirects disabled. The pinned
// local store is served at the configured Tessera origin; unsupported external
// providers require an explicit origin policy rather than arbitrary URL fetches.
func (t *Tessera) attachmentBytes(ctx context.Context, token string, ref contentReference) ([]byte, string, error) {
	var resolved struct {
		URL, Method string
		Headers     map[string]string
		ContentType string `json:"content_type"`
	}
	path := "/v1/stores/" + url.PathEscape(ref.Store) + "/items/" + url.PathEscape(ref.Item) + ":resolve"
	if e := t.post(ctx, path, token, map[string]any{"representation": "original"}, &resolved); e != nil {
		return nil, "", errors.New("attachment unavailable")
	}
	endpoint, e := url.Parse(resolved.URL)
	origin, _ := url.Parse(t.internal)
	public, _ := url.Parse(t.public)
	sameOrigin := func(a, b *url.URL) bool {
		return a != nil && b != nil && a.Scheme == b.Scheme && a.Host == b.Host && a.Host != ""
	}
	if e != nil || endpoint.User != nil || endpoint.Fragment != "" || (!sameOrigin(endpoint, origin) && !sameOrigin(endpoint, public)) || resolved.Method != "" && resolved.Method != "GET" {
		return nil, "", errors.New("unsupported content origin")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", resolved.URL, nil)
	if e != nil {
		return nil, "", errors.New("attachment unavailable")
	}
	for key, value := range resolved.Headers {
		if strings.EqualFold(key, "Host") || strings.ContainsAny(key+value, "\r\n") {
			return nil, "", errors.New("invalid transfer headers")
		}
		req.Header.Set(key, value)
	}
	client := *t.http
	// Native storage advertises the browser origin. Keep the signed URL and
	// Host intact, but reach that same configured service over its internal
	// network address with its normal trusted certificate. Never route a third
	// party origin or forward its credentials to an arbitrary endpoint.
	if !sameOrigin(endpoint, origin) {
		transport, ok := t.http.Transport.(*http.Transport)
		if !ok || origin.Hostname() == "" || origin.Scheme != endpoint.Scheme {
			return nil, "", errors.New("unsupported content transport")
		}
		routed := transport.Clone()
		routed.Proxy = nil
		address := origin.Host
		if origin.Port() == "" {
			port := "443"
			if origin.Scheme == "http" {
				port = "80"
			}
			address = net.JoinHostPort(origin.Hostname(), port)
		}
		routed.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		}
		if routed.TLSClientConfig == nil {
			routed.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		routed.TLSClientConfig.ServerName = origin.Hostname()
		defer routed.CloseIdleConnections()
		client.Transport = routed
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, e := client.Do(req)
	if e != nil {
		return nil, "", errors.New("attachment unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, "", errors.New("attachment unavailable")
	}
	const maxAttachment = 32 << 20
	if response.ContentLength > maxAttachment {
		return nil, "", errors.New("attachment too large")
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, maxAttachment+1))
	if e != nil || len(data) > maxAttachment {
		return nil, "", errors.New("attachment unavailable")
	}
	kind, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	return data, kind, nil
}
func (a *app) attachment(w http.ResponseWriter, r *http.Request) {
	sess, scope, src, rec, ok := a.readableRecord(w, r)
	if !ok {
		return
	}
	attachments, _ := a.recordAttachments(r.Context(), scope, src, rec)
	for _, attachment := range attachments {
		if attachment.Key != r.URL.Query().Get("attachment") {
			continue
		}
		token, e := src.Token(r.Context())
		if e != nil {
			break
		}
		data, kind, e := a.tessera.attachmentBytes(r.Context(), token, attachment.content)
		if e != nil {
			break
		}
		if r.URL.Query().Get("preview") == "1" {
			if !imageMIME(kind) || !imageMIME(http.DetectContentType(data)) {
				break
			}
			w.Header().Set("Content-Type", kind)
			w.Header().Set("Content-Disposition", "inline")
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": attachment.Name}))
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Write(data)
		return
	}
	a.render(w, r, sess, "message", http.StatusNotFound, map[string]string{"Key": "error", "Retry": recordURL(scope, rec), "RetryLabel": "back"})
}

func recordMapKey(r readRecord) string { return r.Dataset + "\x00" + r.Type + "\x00" + r.ID }

// Badges count authorized links, not resolved content. Fetch relation pages once
// per dataset, not once per row; a badge grants no access to the original.
func (a *app) attachmentBadges(ctx context.Context, sources []Source, rows []readRecord) map[string]string {
	seen := map[string]map[string]bool{}
	byDataset := map[string]bool{}
	partial := map[string]bool{}
	add := func(key, ref string) {
		if seen[key] == nil {
			seen[key] = map[string]bool{}
		}
		seen[key][ref] = true
	}
	for _, r := range rows {
		key := recordMapKey(r)
		seen[key] = map[string]bool{}
		byDataset[r.Dataset] = true
		for _, f := range r.Fields {
			if f.Type != "media" {
				continue
			}
			text, _ := r.Values[f.ID].(string)
			if ref, ok := directContent(text); ok {
				add(key, "content:"+attachmentKey(ref))
			} else if ref, ok := mediaReference(text); ok {
				add(key, "object:"+ref.Dataset+"\x00"+ref.Type+"\x00"+ref.ID)
			}
		}
	}
	for _, src := range sources {
		if !byDataset[src.Dataset] {
			continue
		}
		token, e := src.Token(ctx)
		if e != nil {
			partial[src.Dataset] = true
			continue
		}
		var metas []Meta
		e = retryRefused(ctx, src, &token, func(tok string) error { var err error; metas, err = a.tessera.types(ctx, tok, src.Dataset); return err })
		if e != nil {
			partial[src.Dataset] = true
			continue
		}
		has := false
		for _, m := range metas {
			if m.ID == "com.voptrack.media_attachment" {
				has = true
			}
		}
		if !has {
			continue
		}
		links, cut, e := a.tessera.recordRows(ctx, token, src.Dataset, "com.voptrack.media_attachment", map[string]any{"type_scope": map[string]any{"kind": "exact"}}, recordReadLimit)
		if e != nil || cut {
			partial[src.Dataset] = true
		}
		for _, link := range links {
			raw, _ := json.Marshal(link.Values["subject"])
			var subject objectReference
			if json.Unmarshal(raw, &subject) != nil {
				continue
			}
			key := recordMapKey(readRecord{Dataset: subject.Dataset, Type: subject.Type, ID: subject.ID})
			if _, ok := seen[key]; !ok {
				continue
			}
			raw, _ = json.Marshal(link.Values["media"])
			var media objectReference
			if json.Unmarshal(raw, &media) != nil || media.ID == "" {
				continue
			}
			add(key, "object:"+media.Dataset+"\x00"+media.Type+"\x00"+media.ID)
		}
	}
	out := map[string]string{}
	for _, r := range rows {
		key := recordMapKey(r)
		n := len(seen[key])
		if n > 0 {
			out[key] = fmt.Sprintf("%d attachment", n)
			if n != 1 {
				out[key] += "s"
			}
			if partial[r.Dataset] {
				out[key] += " · more may be unavailable"
			}
		} else if partial[r.Dataset] {
			out[key] = "Attachment information unavailable"
		}
	}
	return out
}
