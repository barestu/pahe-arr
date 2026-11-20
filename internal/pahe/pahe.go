// Package pahe talks to animepahe: search, episode list and per-episode kwik sources.
// Endpoints and parsing are ported from barestu/animepahe-browser-dl (MIT), src/lib/animepahe.ts.
package pahe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pahe-arr/internal/session"
)

type Anime struct {
	Session  string
	Title    string
	Type     string
	Status   string
	Year     string
	Episodes string
}

type Episode struct {
	AnimeSession string
	Session      string
	Number       string
}

// Source is one stream option on a play page.
type Source struct {
	Quality int    // vertical resolution, e.g. 1080
	Audio   string // "jpn" or "eng"
	KwikURL string
}

type Client struct {
	S     *session.Session
	Delay time.Duration // pause between paginated requests, to stay polite
}

func (c *Client) base() string { return "https://" + c.S.Host }

// Search uses animepahe's search API. Its response shape is not used by the extension,
// so fields are read loosely and anything unexpected is skipped.
func (c *Client) Search(ctx context.Context, q string) ([]Anime, error) {
	body, err := c.S.Get(ctx, c.base()+"/api?m=search&q="+url.QueryEscape(q))
	if err != nil {
		return nil, err
	}
	if looksHTML(body) {
		return nil, fmt.Errorf("search returned HTML instead of JSON (likely a Cloudflare page)")
	}
	var r struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("search: unexpected response: %w", err)
	}
	var out []Anime
	for _, d := range r.Data {
		a := Anime{
			Session:  str(d["session"]),
			Title:    html.UnescapeString(str(d["title"])),
			Type:     str(d["type"]),
			Status:   str(d["status"]),
			Year:     str(d["year"]),
			Episodes: str(d["episodes"]),
		}
		if a.Session != "" {
			out = append(out, a)
		}
	}
	return out, nil
}

var (
	titleRe      = regexp.MustCompile(`(?is)<title>\s*(.*?)\s*(?:::|\||</title>)`)
	animeInfoRe  = regexp.MustCompile(`(?i)\s*anime info.*$`)
	epSuffixRe   = regexp.MustCompile(`(?i)\s*\bEp\.?\s.*$`)
	trailBrackRe = regexp.MustCompile(`\s*\[[^\]]*\]\s*$`)
)

// AnimeTitle reads the title from the /anime/<session> page.
func (c *Client) AnimeTitle(ctx context.Context, animeSession string) (string, error) {
	body, err := c.S.Get(ctx, c.base()+"/anime/"+animeSession)
	if err != nil {
		return "", err
	}
	raw := "Unknown"
	if m := titleRe.FindSubmatch(body); m != nil {
		raw = html.UnescapeString(string(m[1]))
	}
	return CleanTitle(raw), nil
}

// CleanTitle strips "anime info", "Ep. 1-13 [Completed]" and similar suffixes.
func CleanTitle(raw string) string {
	s := animeInfoRe.ReplaceAllString(raw, "")
	s = epSuffixRe.ReplaceAllString(s, "")
	s = trailBrackRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// Episodes returns every episode, following the release API's pagination.
func (c *Client) Episodes(ctx context.Context, animeSession string) ([]Episode, error) {
	var out []Episode
	for page, last := 1, 1; page <= last; page++ {
		u := fmt.Sprintf("%s/api?m=release&id=%s&sort=episode_asc&page=%d", c.base(), url.QueryEscape(animeSession), page)
		body, err := c.S.Get(ctx, u)
		if err != nil {
			return nil, err
		}
		if looksHTML(body) {
			return nil, fmt.Errorf("episode list returned HTML instead of JSON (likely a Cloudflare page)")
		}
		var r struct {
			Data     []map[string]any `json:"data"`
			LastPage float64          `json:"last_page"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, fmt.Errorf("episode list: unexpected response: %w", err)
		}
		if r.LastPage > 0 {
			last = int(r.LastPage)
		}
		for _, d := range r.Data {
			out = append(out, Episode{AnimeSession: animeSession, Session: str(d["session"]), Number: str(d["episode"])})
		}
		if page < last && c.Delay > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.Delay):
			}
		}
	}
	return out, nil
}

// PlayPage fetches /play/<anime>/<episode> and parses its sources. The raw HTML is returned
// so a zero-source result can be diagnosed without fetching again.
func (c *Client) PlayPage(ctx context.Context, animeSession, episodeSession string) ([]Source, string, error) {
	body, err := c.S.Get(ctx, fmt.Sprintf("%s/play/%s/%s", c.base(), animeSession, episodeSession))
	if err != nil {
		return nil, "", err
	}
	h := string(body)
	return ParsePlaySources(h), h, nil
}

var (
	buttonRe = regexp.MustCompile(`(?i)<button\b[^>]*\bdata-src=["']([^"']+)["'][^>]*>`)
	resRe    = regexp.MustCompile(`(?i)data-resolution=["']([^"']+)["']`)
	audioRe  = regexp.MustCompile(`(?i)data-audio=["']([^"']+)["']`)
)

// ParsePlaySources reads the resolution menu buttons (data-src, data-resolution, data-audio).
// This is the markup most likely to change when animepahe updates its site.
func ParsePlaySources(h string) []Source {
	var out []Source
	for _, m := range buttonRe.FindAllStringSubmatch(h, -1) {
		tag, src := m[0], html.UnescapeString(m[1])
		if !strings.Contains(src, "kwik.") {
			continue
		}
		res := ""
		if r := resRe.FindStringSubmatch(tag); r != nil {
			res = strings.TrimSuffix(strings.ToLower(r[1]), "p")
		}
		// Accept any 2-4 digit height: movies and some encodes use off-ladder heights.
		q, err := strconv.Atoi(res)
		if err != nil || len(res) < 2 || len(res) > 4 {
			continue
		}
		audio := "jpn"
		if a := audioRe.FindStringSubmatch(tag); a != nil && strings.HasPrefix(strings.ToLower(a[1]), "eng") {
			audio = "eng"
		}
		out = append(out, Source{Quality: q, Audio: audio, KwikURL: src})
	}
	return out
}

// PickSource prefers the requested audio, then the exact quality, else the nearest one.
func PickSource(sources []Source, quality int, audio string) (Source, bool) {
	if len(sources) == 0 {
		return Source{}, false
	}
	var pool []Source
	for _, s := range sources {
		if s.Audio == audio {
			pool = append(pool, s)
		}
	}
	if len(pool) == 0 {
		pool = sources
	}
	best := pool[0]
	for _, s := range pool[1:] {
		if absInt(s.Quality-quality) < absInt(best.Quality-quality) {
			best = s
		}
	}
	return best, true
}

var (
	cfPageRe    = regexp.MustCompile(`(?i)cf-browser-verification|Just a moment|cf_chl_opt`)
	pageTitleRe = regexp.MustCompile(`(?is)<title>\s*(.*?)\s*</title>`)
)

// DescribePlayPage summarizes why a play page yielded no sources, without dumping the page.
func DescribePlayPage(h string) string {
	marks := []string{
		fmt.Sprintf("len=%d", len(h)),
		fmt.Sprintf("resolutionMenu=%t", strings.Contains(strings.ToLower(h), "resolutionmenu")),
		fmt.Sprintf("data-src=%d", strings.Count(strings.ToLower(h), "data-src=")),
		fmt.Sprintf("kwik=%d", strings.Count(strings.ToLower(h), "kwik.")),
	}
	if cfPageRe.MatchString(h) {
		marks = append(marks, "cloudflare=true")
	}
	if m := pageTitleRe.FindStringSubmatch(h); m != nil {
		t := m[1]
		if len(t) > 60 {
			t = t[:60]
		}
		marks = append(marks, fmt.Sprintf("title=%q", t))
	}
	return strings.Join(marks, " ")
}

func looksHTML(b []byte) bool { return bytes.HasPrefix(bytes.TrimSpace(b), []byte("<")) }

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == math.Trunc(t) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
