package main

import (
	"fmt"
	"net/url"
	"strings"

	"rsc.io/qr"
)

// Connecting the app by scanning a code. The format was agreed with the app's
// maintainers on 2026-09-27:
//
//	voptrack://connect?server=<https origin>&name=<display name>&product=<home|study>
//
// `product`, added 2026-09-27 at the app team's request, says which Voptrack
// product the server is -- `home` for a household, `study` for Voptrack
// Research -- so the app can set itself up before signing in. It is absent on
// a personal server. It is a hint, never trusted: what a store holds decides.
//
// The code says only where: no token, no account, no password. The person still
// signs in through Tessera's own consent, so a code photographed off a fridge
// gives nobody anything. `server` is an origin -- scheme, host, optional port
// -- and the app refuses anything else, and any non-https origin that is not
// loopback, before making a request. It confirms the host with the person
// before connecting, so `name` is display text that cannot impersonate a server.
//
// rsc.io/qr is the Console's one dependency outside the standard library: a QR
// encoder is Reed-Solomon arithmetic, which is exactly where a hand-written bug
// would pass every test that did not scan the result.

// productOf is the connect link's word for a deployment's variant.
func productOf(variant string) string {
	switch variant {
	case "household":
		return "home"
	case "research":
		return "study"
	}
	return ""
}

// pin, when the server has no publicly trusted certificate, is its own CA
// root's key -- sha256/<base64 SPKI> -- which the app pins for that host.
func connectLink(public, name, product, pin string) (string, error) {
	u, err := url.Parse(public)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("the public address %q is not an https origin", public)
	}
	origin := u.Scheme + "://" + u.Host // strips any path, query or userinfo
	q := url.Values{"server": {origin}}
	if name = strings.TrimSpace(name); name != "" {
		q.Set("name", name)
	}
	if product != "" {
		q.Set("product", product)
	}
	if pin != "" {
		q.Set("pin", pin) // percent-encoded: / + = become %2F %2B %3D
	}
	// Spaces as %20, never "+": Go's form encoding writes "+", and the app's
	// URLComponents reads a "+" in a query as a literal plus, so "The Rivers"
	// would arrive as "The+Rivers". %20 is read the same way everywhere.
	return "voptrack://connect?" + strings.ReplaceAll(q.Encode(), "+", "%20"), nil
}

// QR is a code drawn as one SVG path, a square per dark module, with the quiet
// zone the standard requires around it.
type QR struct {
	Path string
	Size int
}

func qrSVG(text string) (QR, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return QR{}, err
	}
	const quiet = 4
	var b strings.Builder
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	return QR{Path: b.String(), Size: code.Size + 2*quiet}, nil
}

// withPin adds the pin to a link the app may open, such as an invitation, so
// every way into a local Home carries the key to trust. Unchanged without one.
func withPin(link, pin string) string {
	if pin == "" || link == "" {
		return link
	}
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	q := u.Query()
	q.Set("pin", pin)
	u.RawQuery = q.Encode()
	return u.String()
}
