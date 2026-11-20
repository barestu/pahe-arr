// Package kwik turns a kwik player URL into its HLS (.m3u8) URL without a browser:
// it fetches the player page and decodes the packed script that holds the stream URL.
package kwik

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Result is the resolved stream plus the kwik origin the CDN expects as Referer/Origin.
type Result struct {
	M3U8   string
	Origin string // e.g. https://kwik.cx
}

// ErrChallenge means kwik answered with a Cloudflare challenge, which plain HTTP cannot pass.
var ErrChallenge = errors.New("kwik returned a Cloudflare challenge")

var (
	packedStartRe = regexp.MustCompile(`eval\(function\(p,a,c,k,e,[dr]\)`)
	sourceRe      = regexp.MustCompile(`source\s*=\s*['"]([^'"]+\.m3u8[^'"]*)['"]`)
	anyM3U8Re     = regexp.MustCompile(`https?://[^'"\s\\]+\.m3u8[^'"\s\\]*`)
	challengeRe   = regexp.MustCompile(`(?i)cf-browser-verification|Just a moment|cf_chl_opt|Attention Required`)
)

// Resolve fetches the kwik page with Referer set to animepahe (kwik only allows the embed
// from there) and extracts the m3u8 URL.
func Resolve(ctx context.Context, client *http.Client, kwikURL, animepaheHost, userAgent string) (*Result, error) {
	u, err := url.Parse(kwikURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("bad kwik URL %q", kwikURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kwikURL, nil)
	if err != nil {
		return nil, err
	}
	referer, reqOrigin := "https://"+animepaheHost+"/", "https://"+animepaheHost
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)
	req.Header.Set("Origin", reqOrigin)

	// If kwik redirects to another kwik host, keep Referer/Origin pointing at animepahe.
	cl := *client
	cl.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		r.Header.Set("Referer", referer)
		r.Header.Set("Origin", reqOrigin)
		return nil
	}
	res, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 5<<20))
	if err != nil {
		return nil, err
	}
	if (res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusServiceUnavailable) && challengeRe.Match(body) {
		return nil, ErrChallenge
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kwik page: HTTP %d", res.StatusCode)
	}

	m3u8, err := ExtractM3U8(string(body))
	if err != nil {
		return nil, err
	}
	// The CDN checks Referer/Origin against the kwik host that actually served the player,
	// which is the final URL after any redirect.
	final := res.Request.URL
	return &Result{M3U8: m3u8, Origin: final.Scheme + "://" + final.Host}, nil
}

// Headers are the request headers the CDN expects for the playlist, key and segments.
func (r *Result) Headers(userAgent string) http.Header {
	h := http.Header{}
	h.Set("User-Agent", userAgent)
	h.Set("Accept", "*/*")
	h.Set("Referer", r.Origin+"/")
	h.Set("Origin", r.Origin)
	return h
}

// ExtractM3U8 decodes every packed script on the page and returns the first stream URL found.
func ExtractM3U8(page string) (string, error) {
	for _, loc := range packedStartRe.FindAllStringIndex(page, -1) {
		script := page[loc[0]:]
		if end := strings.Index(script, "</script>"); end >= 0 {
			script = script[:end]
		}
		unpacked, err := Unpack(script)
		if err != nil {
			continue
		}
		if m := sourceRe.FindStringSubmatch(unpacked); m != nil {
			return m[1], nil
		}
		if m := anyM3U8Re.FindString(unpacked); m != "" {
			return m, nil
		}
	}
	// Fallback: the URL might be in the page as plain text.
	if m := sourceRe.FindStringSubmatch(page); m != nil {
		return m[1], nil
	}
	if m := anyM3U8Re.FindString(page); m != "" {
		return m, nil
	}
	return "", errors.New("no m3u8 found in the kwik page (its player script may have changed)")
}
