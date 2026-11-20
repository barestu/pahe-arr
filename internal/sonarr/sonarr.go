// Package sonarr hands downloaded episodes to Sonarr through its v3 API: it reads Sonarr's own
// view of the file (GET /manualimport), then runs the ManualImport command with the exact
// series and episode, in move mode, and waits for the command to finish.
package sonarr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	// PollInterval between command status checks (default 1s).
	PollInterval time.Duration
}

func New(baseURL, apiKey string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "Sonarr rejected the API key (HTTP 401)"
	case http.StatusNotFound:
		return "Sonarr endpoint not found (HTTP 404): check the URL"
	}
	return fmt.Sprintf("Sonarr returned HTTP %d: %s", e.Status, e.Body)
}

func (c *Client) do(ctx context.Context, method, p string, q url.Values, body, out any) error {
	u := c.BaseURL + "/api/v3" + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.APIKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Sonarr unreachable: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		snippet := strings.TrimSpace(string(data))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return &APIError{Status: res.StatusCode, Body: snippet}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("unexpected Sonarr response for %s: %w", p, err)
	}
	return nil
}

type Status struct {
	AppName string `json:"appName"`
	Version string `json:"version"`
}

func (c *Client) Status(ctx context.Context) (*Status, error) {
	var s Status
	if err := c.do(ctx, http.MethodGet, "/system/status", nil, nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

type Series struct {
	ID              int    `json:"id"`
	Title           string `json:"title"`
	Year            int    `json:"year"`
	SeriesType      string `json:"seriesType"`
	Path            string `json:"path"`
	AlternateTitles []struct {
		Title string `json:"title"`
	} `json:"alternateTitles"`
}

func (c *Client) Series(ctx context.Context) ([]Series, error) {
	var out []Series
	err := c.do(ctx, http.MethodGet, "/series", nil, nil, &out)
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out, err
}

type Episode struct {
	ID                    int    `json:"id"`
	SeasonNumber          int    `json:"seasonNumber"`
	EpisodeNumber         int    `json:"episodeNumber"`
	AbsoluteEpisodeNumber *int   `json:"absoluteEpisodeNumber"`
	Title                 string `json:"title"`
	HasFile               bool   `json:"hasFile"`
}

func (c *Client) Episodes(ctx context.Context, seriesID int) ([]Episode, error) {
	var out []Episode
	err := c.do(ctx, http.MethodGet, "/episode", url.Values{"seriesId": {strconv.Itoa(seriesID)}}, nil, &out)
	return out, err
}

// Mapping turns an animepahe episode number into a Sonarr episode.
// Season 0 means absolute numbering (anime); otherwise the number is matched within that season.
// Offset is added first, e.g. animepahe's "Season 2" entry episode 1 = absolute 26 → offset 25.
type Mapping struct {
	Season int
	Offset int
}

func (m Mapping) Describe() string {
	s := "absolute"
	if m.Season > 0 {
		s = fmt.Sprintf("season %d", m.Season)
	}
	if m.Offset != 0 {
		s += fmt.Sprintf(", offset %+d", m.Offset)
	}
	return s
}

// FindEpisode locates the Sonarr episode for an animepahe episode number.
func FindEpisode(eps []Episode, number string, m Mapping) (Episode, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
	if err != nil || f != math.Trunc(f) {
		return Episode{}, fmt.Errorf("episode %q can't be mapped to Sonarr (not a whole number)", number)
	}
	n := int(f) + m.Offset
	for _, e := range eps {
		if m.Season == 0 {
			if e.AbsoluteEpisodeNumber != nil && *e.AbsoluteEpisodeNumber == n {
				return e, nil
			}
		} else if e.SeasonNumber == m.Season && e.EpisodeNumber == n {
			return e, nil
		}
	}
	return Episode{}, fmt.Errorf("Sonarr has no episode for %s → %d (%s)", number, n, m.Describe())
}

type rejection struct {
	Reason string `json:"reason"`
}

// candidate is Sonarr's view of a file in the import folder. Quality and languages are passed
// back unchanged so the import uses what Sonarr itself detected.
type candidate struct {
	Path         string          `json:"path"`
	FolderName   string          `json:"folderName"`
	ReleaseGroup string          `json:"releaseGroup"`
	Quality      json.RawMessage `json:"quality"`
	Languages    json.RawMessage `json:"languages"`
	IndexerFlags int             `json:"indexerFlags"`
	DownloadID   string          `json:"downloadId"`
	Rejections   []rejection     `json:"rejections"`
}

type command struct {
	ID         int    `json:"id"`
	Status     string `json:"status"`
	Result     string `json:"result"`
	Message    string `json:"message"`
	Exception  string `json:"exception"`
	Completion string `json:"completionMessage"`
}

// ImportFile moves one file (path as Sonarr sees it) into the series as the given episode.
func (c *Client) ImportFile(ctx context.Context, filePath string, seriesID, episodeID int) error {
	folder := path.Dir(filePath)
	var cands []candidate
	q := url.Values{"folder": {folder}, "filterExistingFiles": {"false"}}
	if err := c.do(ctx, http.MethodGet, "/manualimport", q, nil, &cands); err != nil {
		return fmt.Errorf("Sonarr could not scan %s: %w", folder, err)
	}
	var cand *candidate
	for i := range cands {
		if path.Clean(cands[i].Path) == path.Clean(filePath) {
			cand = &cands[i]
			break
		}
	}
	if cand == nil {
		return fmt.Errorf("Sonarr does not see %s: check that Sonarr mounts the same folder (Settings → output folder as seen by Sonarr)", filePath)
	}

	file := map[string]any{
		"path":         cand.Path,
		"folderName":   cand.FolderName,
		"seriesId":     seriesID,
		"episodeIds":   []int{episodeID},
		"releaseGroup": cand.ReleaseGroup,
		"indexerFlags": cand.IndexerFlags,
		"releaseType":  "singleEpisode",
		"downloadId":   cand.DownloadID,
	}
	if file["releaseGroup"] == "" {
		file["releaseGroup"] = "AnimePahe"
	}
	if len(cand.Quality) > 0 && string(cand.Quality) != "null" {
		file["quality"] = cand.Quality
	}
	if len(cand.Languages) > 0 && string(cand.Languages) != "null" {
		file["languages"] = cand.Languages
	}
	body := map[string]any{"name": "ManualImport", "importMode": "move", "files": []any{file}}

	var cmd command
	if err := c.do(ctx, http.MethodPost, "/command", nil, body, &cmd); err != nil {
		return fmt.Errorf("Sonarr refused the import: %w", err)
	}
	if err := c.wait(ctx, &cmd); err != nil {
		return err
	}
	if len(cand.Rejections) > 0 {
		// Not fatal by itself: a manual import with explicit IDs can override most rejections.
		// The caller checks whether the file actually moved.
		return &RejectedError{Reasons: reasons(cand.Rejections)}
	}
	return nil
}

// RejectedError carries Sonarr's rejection reasons. Callers treat it as a warning when the
// file was moved anyway, and as the explanation when it wasn't.
type RejectedError struct{ Reasons []string }

func (e *RejectedError) Error() string {
	return "Sonarr flagged the file: " + strings.Join(e.Reasons, "; ")
}

func reasons(rs []rejection) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Reason)
	}
	return out
}

func (c *Client) wait(ctx context.Context, cmd *command) error {
	interval := c.PollInterval
	if interval == 0 {
		interval = time.Second
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		switch strings.ToLower(cmd.Status) {
		case "completed":
			if strings.EqualFold(cmd.Result, "unsuccessful") {
				return fmt.Errorf("Sonarr import finished unsuccessfully: %s", firstNonEmpty(cmd.Message, cmd.Exception))
			}
			return nil
		case "failed", "aborted", "cancelled", "orphaned":
			return fmt.Errorf("Sonarr import %s: %s", strings.ToLower(cmd.Status), firstNonEmpty(cmd.Exception, cmd.Message, "no details"))
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for Sonarr to finish the import")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		if err := c.do(ctx, http.MethodGet, "/command/"+strconv.Itoa(cmd.ID), nil, nil, cmd); err != nil {
			return err
		}
	}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ---------- series suggestion ----------

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func normalize(s string) string {
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

func similarity(a, b string) float64 {
	na, nb := normalize(a), normalize(b)
	if na == "" || nb == "" {
		return 0
	}
	if na == nb {
		return 1
	}
	if strings.Contains(na, nb) || strings.Contains(nb, na) {
		return 0.85
	}
	ta, tb := strings.Fields(na), strings.Fields(nb)
	set := map[string]bool{}
	for _, t := range ta {
		set[t] = true
	}
	inter := 0
	union := len(set)
	seen := map[string]bool{}
	for _, t := range tb {
		if seen[t] {
			continue
		}
		seen[t] = true
		if set[t] {
			inter++
		} else {
			union++
		}
	}
	return 0.8 * float64(inter) / float64(union)
}

// Suggest ranks library series by how well their titles (and alternate titles) match.
func Suggest(all []Series, title string, limit int) []Series {
	type scored struct {
		s     Series
		score float64
	}
	var ranked []scored
	for _, s := range all {
		best := similarity(s.Title, title)
		for _, a := range s.AlternateTitles {
			best = math.Max(best, similarity(a.Title, title))
		}
		if best >= 0.3 {
			ranked = append(ranked, scored{s, best})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	out := make([]Series, 0, limit)
	for i := 0; i < len(ranked) && i < limit; i++ {
		out = append(out, ranked[i].s)
	}
	return out
}
