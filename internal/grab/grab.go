// Package grab downloads one animepahe episode to an .mp4: pick a source, resolve kwik,
// download the HLS stream in Go, then remux locally. Used by the CLI and the web job worker.
package grab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"pahe-arr/internal/dl"
	"pahe-arr/internal/hls"
	"pahe-arr/internal/kwik"
	"pahe-arr/internal/pahe"
)

var (
	kwikHTTP = &http.Client{Timeout: 30 * time.Second}
	cdnHTTP  = &http.Client{Timeout: 2 * time.Minute}
)

type Options struct {
	Exact       bool // Sonarr grabs must match the advertised quality and audio.
	OutputDir   string
	Title       string // folder and file title
	Label       string // episode label, e.g. "02"
	Quality     int    // target height; nearest available is used
	Audio       string // "jpn" or "eng"
	Concurrency int
}

type Stage string

const (
	Resolving   Stage = "resolving"
	Downloading Stage = "downloading"
	Remuxing    Stage = "remuxing"
)

// Progress reports the current stage; done/total are segment counts while downloading.
type Progress func(stage Stage, done, total int)

type Result struct {
	Path    string
	Quality int
	Audio   string
	Via     string // "kwik host → stream host"
	Skipped bool   // file already existed
}

// Episode downloads one episode. The .mp4 only appears once complete.
func Episode(ctx context.Context, c *pahe.Client, ep pahe.Episode, o Options, progress Progress) (*Result, error) {
	if progress == nil {
		progress = func(Stage, int, int) {}
	}
	progress(Resolving, 0, 0)
	sources, page, err := c.PlayPage(ctx, ep.AnimeSession, ep.Session)
	if err != nil {
		return nil, err
	}
	src, ok := pahe.PickSource(sources, o.Quality, o.Audio)
	if !ok {
		return nil, fmt.Errorf("no streams on play page (%s)", pahe.DescribePlayPage(page))
	}
	if o.Exact && (src.Quality != o.Quality || src.Audio != o.Audio) {
		return nil, fmt.Errorf("selected %dp %s stream is no longer available", o.Quality, o.Audio)
	}
	title := SafeName(o.Title)
	label := o.Label
	if label == "" {
		label = Label(ep.Number, 2)
	}
	dir := filepath.Join(o.OutputDir, title)
	dest := filepath.Join(dir, FileName(title, label, src.Quality, src.Audio))
	r := &Result{Path: dest, Quality: src.Quality, Audio: src.Audio}
	if _, err := os.Stat(dest); err == nil {
		r.Skipped = true
		return r, nil
	}

	res, err := kwik.Resolve(ctx, kwikHTTP, src.KwikURL, c.S.Host, c.S.UserAgent())
	if err != nil {
		return nil, err
	}
	r.Via = HostOf(res.Origin) + " → " + HostOf(res.M3U8)

	if err := os.MkdirAll(dir, 0o775); err != nil {
		return nil, err
	}
	// Hidden .part temp file on the same disk: Sonarr ignores it and the remux stays local.
	tmp := filepath.Join(dir, "."+strings.TrimSuffix(filepath.Base(dest), ".mp4")+".ts.part")
	defer os.Remove(tmp)
	tf, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	f := &hls.Fetcher{Client: cdnHTTP, Header: res.Headers(c.S.UserAgent())}
	err = hls.Download(ctx, f, res.M3U8, tf, o.Concurrency, func(done, total int) {
		progress(Downloading, done, total)
	})
	if cerr := tf.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	progress(Remuxing, 0, 0)
	if err := dl.Remux(ctx, tmp, dest, nil); err != nil {
		return nil, err
	}
	return r, nil
}

// FileName follows the common anime release pattern, which Sonarr parses as absolute numbering.
func FileName(title, label string, quality int, audio string) string {
	name := fmt.Sprintf("[AnimePahe] %s - %s [%dp]", title, label, quality)
	if audio == "eng" {
		name += " [Dub]"
	}
	return name + ".mp4"
}

// Label pads whole episode numbers to the given width (decimals like 12.5 stay as-is).
func Label(num string, width int) string {
	if n, err := strconv.Atoi(num); err == nil {
		return fmt.Sprintf("%0*d", width, n)
	}
	return num
}

// LabelWidth is at least 2, or wider for long series (e.g. 3 when episode 100+ exists).
func LabelWidth(eps []pahe.Episode) int {
	width := 2
	for _, e := range eps {
		if n, err := strconv.Atoi(e.Number); err == nil && len(strconv.Itoa(n)) > width {
			width = len(strconv.Itoa(n))
		}
	}
	return width
}

var unsafeRe = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]+`)

func SafeName(s string) string {
	s = unsafeRe.ReplaceAllString(s, " ")
	s = strings.Trim(strings.Join(strings.Fields(s), " "), ". ")
	if s == "" {
		return "Unknown"
	}
	return s
}

func HostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "?"
}
