package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pahe-arr/internal/grab"
	"pahe-arr/internal/jobs"
	"pahe-arr/internal/pahe"
)

type sonarrBridge struct {
	s *Server
}

func (b sonarrBridge) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := b.s.Settings.Get().IntegrationKey
		if key == "" || !hmac.Equal([]byte(r.URL.Query().Get("apikey")), []byte(key)) {
			http.Error(w, "invalid integration key", 401)
			return
		}
		if !sameOrigin(r) {
			http.Error(w, "cross-site request blocked", 403)
			return
		}
		next(w, r)
	}
}

type release struct {
	Title, Anime, Episode, Number, Audio string
	Height                               int
}

func (v release) name() string {
	audio := "Sub"
	if v.Audio == "eng" {
		audio = "Dub"
	}
	return fmt.Sprintf("[AnimePahe] %s - %s [%dp] [%s]", grab.SafeName(v.Title), grab.Label(v.Number, 2), v.Height, audio)
}
func (b sonarrBridge) token(v release) string {
	raw, _ := json.Marshal(v)
	mac := hmac.New(sha256.New, b.s.Secret)
	mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(append(mac.Sum(nil), raw...))
}
func (b sonarrBridge) unpack(token string) (release, bool) {
	var v release
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) <= sha256.Size {
		return v, false
	}
	mac := hmac.New(sha256.New, b.s.Secret)
	mac.Write(raw[sha256.Size:])
	if !hmac.Equal(raw[:sha256.Size], mac.Sum(nil)) {
		return v, false
	}
	err = json.Unmarshal(raw[sha256.Size:], &v)
	return v, err == nil
}

type catalogue interface {
	Search(context.Context, string) ([]pahe.Anime, error)
	Episodes(context.Context, string) ([]pahe.Episode, error)
	PlayPage(context.Context, string, string) ([]pahe.Source, string, error)
	LatestEpisode(context.Context) (string, pahe.Episode, error)
}

var episodeQuery = regexp.MustCompile(`^(.+)\s+([0-9]+)$`)
var separators = regexp.MustCompile(`[^\pL\pN]+`)

func titleKey(s string) string {
	return strings.TrimSpace(separators.ReplaceAllString(strings.ToLower(s), " "))
}

func searchReleases(ctx context.Context, c catalogue, q url.Values) ([]release, error) {
	var title string
	var ep pahe.Episode
	if q.Get("offset") != "" && q.Get("offset") != "0" {
		return nil, nil
	}
	term := strings.TrimSpace(q.Get("q"))
	if term == "" {
		if q.Get("ep") != "" || q.Get("season") != "" {
			return nil, nil
		}
		var err error
		title, ep, err = c.LatestEpisode(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		match := episodeQuery.FindStringSubmatch(term)
		if q.Get("t") != "search" || len(match) != 3 {
			log.Printf("Sonarr search skipped: expected a title and absolute episode number (%q)", term)
			return nil, nil
		}
		term = strings.TrimSpace(match[1])
		n, err := strconv.Atoi(match[2])
		if err != nil || n < 1 {
			return nil, nil
		}
		hits, err := c.Search(ctx, term)
		if err != nil {
			return nil, err
		}
		var series string
		for _, hit := range hits {
			if titleKey(hit.Title) == titleKey(term) {
				if series != "" && series != hit.Session {
					log.Printf("Sonarr search skipped: ambiguous title %q", term)
					return nil, nil
				}
				title, series = hit.Title, hit.Session
			}
		}
		if series == "" {
			log.Printf("Sonarr search skipped: no exact title %q", term)
			return nil, nil
		}
		eps, err := c.Episodes(ctx, series)
		if err != nil {
			return nil, err
		}
		for _, candidate := range eps {
			number, err := strconv.Atoi(candidate.Number)
			if err == nil && number == n {
				if ep.Session != "" || candidate.Session == "" {
					log.Printf("Sonarr search skipped: ambiguous episode %d of %q", n, title)
					return nil, nil
				}
				ep = candidate
			}
		}
		if ep.Session == "" {
			log.Printf("Sonarr search skipped: episode %d missing for %q", n, title)
			return nil, nil
		}
	}
	sources, _, err := c.PlayPage(ctx, ep.AnimeSession, ep.Session)
	if err != nil {
		return nil, err
	}
	var out []release
	seen := map[release]bool{}
	for _, src := range sources {
		v := release{title, ep.AnimeSession, ep.Session, ep.Number, src.Audio, src.Quality}
		if !seen[v] {
			out = append(out, v)
			seen[v] = true
		}
	}
	return out, nil
}

const caps = `<caps><server title="pahe-arr"/><limits max="100" default="100"/><searching><search available="yes" supportedParams="q" searchEngine="raw"/><tv-search available="no"/></searching><categories><category id="5000" name="TV"><subcat id="5070" name="TV/Anime"/></category></categories></caps>`

type feedItem struct {
	Title     string `xml:"title"`
	GUID      string `xml:"guid"`
	Link      string `xml:"link"`
	Date      string `xml:"pubDate"`
	Category  string `xml:"category"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Type   string `xml:"type,attr"`
		Length int    `xml:"length,attr"`
	} `xml:"enclosure"`
}
type descriptor struct {
	XMLName   xml.Name `xml:"nzb"`
	Namespace string   `xml:"xmlns,attr,omitempty"`
	Token     string   `xml:"file>segments>segment"`
}

func (b sonarrBridge) indexer(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "GET required", 405)
		return
	}
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/xml")
	switch q.Get("t") {
	case "caps":
		fmt.Fprint(w, caps)
	case "get":
		token := q.Get("id")
		if _, ok := b.unpack(token); !ok {
			http.Error(w, "invalid release", 400)
			return
		}
		w.Header().Set("Content-Type", "application/x-nzb")
		xml.NewEncoder(w).Encode(descriptor{Namespace: "http://www.newzbin.com/DTD/2003/nzb", Token: token})
	case "search", "tvsearch":
		releases, err := searchReleases(r.Context(), b.s.Clients.For(b.s.Settings.Get()), q)
		if err != nil {
			http.Error(w, friendly(err), 502)
			return
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		fmt.Fprint(w, `<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/"><channel><title>AnimePahe</title>`)
		fmt.Fprintf(w, `<newznab:response offset="0" total="%d"/>`, len(releases))
		for _, v := range releases {
			token := b.token(v)
			link := url.URL{Scheme: scheme, Host: r.Host, Path: r.URL.Path, RawQuery: url.Values{"t": {"get"}, "apikey": {q.Get("apikey")}, "id": {token}}.Encode()}
			item := feedItem{Title: v.name(), GUID: token, Link: link.String(), Date: time.Now().UTC().Format(time.RFC1123Z), Category: "5070"}
			item.Enclosure.URL, item.Enclosure.Type = item.Link, "application/x-nzb"
			xml.NewEncoder(w).EncodeElement(item, xml.StartElement{Name: xml.Name{Local: "item"}})
		}
		fmt.Fprint(w, "</channel></rss>")
	default:
		http.Error(w, "unsupported indexer operation", 400)
	}
}

func (v release) job() jobs.Job {
	return jobs.Job{AnimeSession: v.Anime, AnimeTitle: v.Title, EpisodeSession: v.Episode, EpisodeNumber: v.Number, EpisodeLabel: grab.Label(v.Number, 2), Quality: v.Height, Audio: v.Audio, ReleaseTitle: v.name()}
}
