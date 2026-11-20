package sonarr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func ip(n int) *int { return &n }

func TestFindEpisode(t *testing.T) {
	eps := []Episode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, AbsoluteEpisodeNumber: ip(1)},
		{ID: 26, SeasonNumber: 2, EpisodeNumber: 1, AbsoluteEpisodeNumber: ip(26)},
	}
	if e, err := FindEpisode(eps, "1", Mapping{}); err != nil || e.ID != 1 {
		t.Errorf("absolute: %v %v", e, err)
	}
	if e, err := FindEpisode(eps, "1", Mapping{Offset: 25}); err != nil || e.ID != 26 {
		t.Errorf("offset: %v %v", e, err)
	}
	if e, err := FindEpisode(eps, "1", Mapping{Season: 2}); err != nil || e.ID != 26 {
		t.Errorf("season: %v %v", e, err)
	}
	if _, err := FindEpisode(eps, "12.5", Mapping{}); err == nil {
		t.Error("decimal episode should not map")
	}
	if _, err := FindEpisode(eps, "3", Mapping{}); err == nil {
		t.Error("missing episode should error")
	}
}

func TestSuggest(t *testing.T) {
	all := []Series{
		{ID: 1, Title: "Attack on Titan", AlternateTitles: []struct {
			Title string `json:"title"`
		}{{Title: "Shingeki no Kyojin"}}},
		{ID: 2, Title: "Death Note"},
		{ID: 3, Title: "Death Parade"},
		{ID: 4, Title: "Breaking Bad"},
	}
	got := Suggest(all, "Death Note", 5)
	if len(got) == 0 || got[0].ID != 2 {
		t.Fatalf("Death Note first: %+v", got)
	}
	got = Suggest(all, "Shingeki no Kyojin Season 2", 5)
	if len(got) == 0 || got[0].ID != 1 {
		t.Fatalf("alternate title match: %+v", got)
	}
	for _, s := range Suggest(all, "Death Note", 5) {
		if s.ID == 4 {
			t.Error("unrelated series suggested")
		}
	}
}

// fakeSonarr checks the import request matches Sonarr's API and reports completion on the 2nd poll.
func fakeSonarr(t *testing.T, gotBody *map[string]any, polls *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "key" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/api/v3/system/status":
			w.Write([]byte(`{"appName":"Sonarr","version":"4.0.15.2941"}`))
		case r.URL.Path == "/api/v3/manualimport":
			if r.URL.Query().Get("folder") != "/DATA/.manual-import/Death Note" || r.URL.Query().Get("filterExistingFiles") != "false" {
				t.Errorf("manualimport query %v", r.URL.Query())
			}
			w.Write([]byte(`[{"path":"/DATA/.manual-import/Death Note/[AnimePahe] Death Note - 02 [1080p].mp4","folderName":"Death Note","releaseGroup":"AnimePahe","quality":{"quality":{"id":3,"name":"WEBDL-1080p"},"revision":{"version":1}},"languages":[{"id":8,"name":"Japanese"}],"indexerFlags":0,"downloadId":"","rejections":[]}]`))
		case r.URL.Path == "/api/v3/command" && r.Method == http.MethodPost:
			json.NewDecoder(r.Body).Decode(gotBody)
			w.WriteHeader(201)
			w.Write([]byte(`{"id":42,"status":"queued"}`))
		case r.URL.Path == "/api/v3/command/42":
			if polls.Add(1) < 2 {
				w.Write([]byte(`{"id":42,"status":"started"}`))
				return
			}
			w.Write([]byte(`{"id":42,"status":"completed","result":"successful"}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestImportFile(t *testing.T) {
	var body map[string]any
	var polls atomic.Int32
	srv := fakeSonarr(t, &body, &polls)
	defer srv.Close()
	c := New(srv.URL+"/", "key")
	c.PollInterval = 10 * time.Millisecond

	st, err := c.Status(context.Background())
	if err != nil || !strings.HasPrefix(st.Version, "4.") {
		t.Fatalf("status %+v %v", st, err)
	}
	if err := c.ImportFile(context.Background(), "/DATA/.manual-import/Death Note/[AnimePahe] Death Note - 02 [1080p].mp4", 7, 102); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "ManualImport" || body["importMode"] != "move" {
		t.Fatalf("command body %v", body)
	}
	f := body["files"].([]any)[0].(map[string]any)
	if f["seriesId"].(float64) != 7 || f["episodeIds"].([]any)[0].(float64) != 102 || f["releaseType"] != "singleEpisode" {
		t.Fatalf("file %v", f)
	}
	if q := f["quality"].(map[string]any)["quality"].(map[string]any)["name"]; q != "WEBDL-1080p" {
		t.Fatalf("quality should be passed through from Sonarr's scan, got %v", q)
	}
	if polls.Load() < 2 {
		t.Error("should poll until completed")
	}

	bad := New(srv.URL, "wrong")
	if _, err := bad.Status(context.Background()); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("wrong key error: %v", err)
	}
	if err := c.ImportFile(context.Background(), "/DATA/.manual-import/Death Note/other.mp4", 7, 102); err == nil || !strings.Contains(err.Error(), "does not see") {
		t.Errorf("unseen file: %v", err)
	}
}
