package web

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
	"pahe-arr/internal/config"
	"pahe-arr/internal/jobs"
	"path/filepath"
	"strings"
	"testing"

	"pahe-arr/internal/pahe"
)

type fixtureCatalogue struct {
	series   []pahe.Anime
	episodes []pahe.Episode
}

func (f fixtureCatalogue) Search(context.Context, string) ([]pahe.Anime, error) { return f.series, nil }
func (f fixtureCatalogue) Episodes(context.Context, string) ([]pahe.Episode, error) {
	return f.episodes, nil
}
func (f fixtureCatalogue) PlayPage(context.Context, string, string) ([]pahe.Source, string, error) {
	return []pahe.Source{{Quality: 1080, Audio: "jpn"}, {Quality: 720, Audio: "eng"}, {Quality: 720, Audio: "eng"}}, "", nil
}
func (f fixtureCatalogue) LatestEpisode(context.Context) (string, pahe.Episode, error) {
	return f.series[0].Title, f.episodes[0], nil
}

func TestNativeGrab(t *testing.T) {
	ts, s := newTestServer(t, "web-password")
	defer ts.Close()
	c := client()
	for _, path := range []string{"/newznab/api?t=caps", "/api?mode=queue", "/sab/api?mode=queue"} {
		res, err := c.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatalf("%s: %d", path, res.StatusCode)
		}
	}

	fixture := fixtureCatalogue{series: []pahe.Anime{{Title: "Example", Session: "series"}}, episodes: []pahe.Episode{{AnimeSession: "series", Session: "episode", Number: "2"}}}
	q := url.Values{"t": {"search"}, "q": {"Example 02"}}
	releases, err := searchReleases(context.Background(), fixture, q)
	if err != nil || len(releases) != 2 || releases[1].Height != 720 || releases[1].Audio != "eng" {
		t.Fatalf("%+v, %v", releases, err)
	}
	fixture.series = append(fixture.series, pahe.Anime{Title: "Example", Session: "remake"})
	ambiguous, err := searchReleases(context.Background(), fixture, q)
	if err != nil || len(ambiguous) != 0 {
		t.Fatalf("ambiguous match: %+v", ambiguous)
	}
	key, err := s.Settings.RotateIntegrationKey()
	if err != nil {
		t.Fatal(err)
	}
	b := sonarrBridge{s: s}
	res, err := c.Get(ts.URL + "/newznab/api?t=get&apikey=" + key + "&id=" + b.token(releases[1]))
	if err != nil {
		t.Fatal(err)
	}
	nzb := body(t, res)
	upload := func(doc string) (int, string) {
		var raw bytes.Buffer
		writer := multipart.NewWriter(&raw)
		part, err := writer.CreateFormFile("name", "Example.nzb")
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte(doc))
		writer.Close()
		res, err := c.Post(ts.URL+"/api?mode=addfile&cat=pahe&apikey="+key, writer.FormDataContentType(), &raw)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, body(t, res)
	}
	if status, _ := upload(strings.Replace(nzb, "<segment>", "<segment>x", 1)); status != 400 {
		t.Fatal("tampered descriptor accepted")
	}
	status, resp := upload(nzb)
	var added struct {
		IDs []string `json:"nzo_ids"`
	}
	if status != 200 || json.Unmarshal([]byte(resp), &added) != nil || len(added.IDs) != 1 {
		t.Fatalf("grab: %d %s", status, resp)
	}
	_, duplicate := upload(nzb)
	if duplicate != resp || len(s.Jobs.List()) != 1 {
		t.Fatal("duplicate grab")
	}
	id := added.IDs[0]
	get := func(mode string) string {
		res, err := c.Get(ts.URL + "/sab/api?apikey=" + key + "&mode=" + mode)
		if err != nil {
			t.Fatal(err)
		}
		return body(t, res)
	}
	if queued := get("queue"); !strings.Contains(queued, id) || !strings.Contains(queued, "720p] [Dub]") {
		t.Fatal(queued)
	}
	s.Jobs.Cancel(id)
	if history := get("history"); !strings.Contains(history, id) || !strings.Contains(history, "Failed") {
		t.Fatal(history)
	}
	get("retry&value=" + id)
	if !strings.Contains(get("queue"), id) {
		t.Fatal("retry missing")
	}
	get("queue&name=delete&value=" + id)
	if strings.Contains(get("history"), id) {
		t.Fatal("removed download visible")
	}
}

// Completed Download Handling consumes the same ID and exact output path after a restart.
func TestCompletedGrabHistory(t *testing.T) {
	dir := t.TempDir()
	settings, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, err := settings.RotateIntegrationKey()
	if err != nil {
		t.Fatal(err)
	}
	settings, err = config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "[AnimePahe] Example - 02 [720p] [Dub].mp4")
	if err := os.WriteFile(output, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	saved := []jobs.Job{{ID: "stable-id", ReleaseTitle: "[AnimePahe] Example - 02 [720p] [Dub]", Status: jobs.Done, Output: output}, {ID: "unrelated", Status: jobs.Done}}
	if err := config.WriteJSONAtomic(filepath.Join(dir, "jobs.json"), saved, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := jobs.NewManager(dir, settings, jobs.NewClients())
	if err != nil {
		t.Fatal(err)
	}
	manager.ClearFinished()
	s := Server{Settings: settings, Jobs: manager}
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/sab/api?mode=history&apikey="+key, nil))
	var response struct {
		History struct {
			Slots []struct {
				ID                        string `json:"nzo_id"`
				Status, Storage, Category string
				Bytes                     int
			}
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	slots := response.History.Slots
	if len(slots) != 1 || slots[0].Status != "Completed" || slots[0].Storage != output || slots[0].Category != "pahe" || slots[0].Bytes != 7 || slots[0].ID != "stable-id" {
		t.Fatal(w.Body.String())
	}
}

func TestWebIntegrationKeyRotation(t *testing.T) {
	ts, s := newTestServer(t, "")
	defer ts.Close()
	c := client()
	stale := s.Settings.Get()
	check := func(key string, status int) {
		t.Helper()
		for _, path := range []string{"/newznab/api?t=caps", "/api?mode=version", "/sab/api?mode=version"} {
			res, err := c.Get(ts.URL + path + "&apikey=" + key)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != status {
				t.Fatalf("%s: got %d, want %d", path, res.StatusCode, status)
			}
		}
	}
	generate := func() string {
		res := post(t, c, ts, "/settings/integration-key", nil, true)
		html := body(t, res)
		key := s.Settings.Get().IntegrationKey
		if len(key) != 64 || strings.Contains(html, key) || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("expected a one-time key response")
		}
		if got := body(t, post(t, c, ts, "/settings/integration-key/copy", nil, true)); got != key {
			t.Fatal("copy returned incorrect key")
		}
		return key
	}
	check("", 401)
	first := generate()
	check(first, 200)
	second := generate()
	if err := s.Settings.Set(stale); err != nil {
		t.Fatal(err)
	}
	check(first, 401)
	check(second, 200)
	res, err := c.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	html := body(t, res)
	if strings.Contains(html, second) || !strings.Contains(html, "Regenerate key") || !strings.Contains(html, strings.Repeat("*", len(second))) {
		t.Fatal("unexpected settings display")
	}
}
