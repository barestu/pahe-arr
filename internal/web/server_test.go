package web

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"pahe-arr/internal/config"
	"pahe-arr/internal/jobs"
)

func newTestServer(t *testing.T, password string) (*httptest.Server, *Server) {
	t.Helper()
	dir := t.TempDir()
	st, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	clients := jobs.NewClients()
	m, err := jobs.NewManager(dir, st, clients)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := LoadSecret(dir)
	s := &Server{Settings: st, Jobs: m, Clients: clients, Password: password, Secret: secret}
	h, err := s.Handler()
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(h), s
}

func client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func post(t *testing.T, c *http.Client, ts *httptest.Server, path string, form url.Values, hx bool) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}

func TestAuth(t *testing.T) {
	ts, _ := newTestServer(t, "s3cret")
	defer ts.Close()
	c := client()

	res, _ := c.Get(ts.URL + "/queue")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
		t.Fatalf("unauthenticated: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/queue/list", nil)
	req.Header.Set("HX-Request", "true")
	res, _ = c.Do(req)
	if res.StatusCode != 401 || res.Header.Get("HX-Redirect") != "/login" {
		t.Fatalf("htmx unauthenticated: %d", res.StatusCode)
	}
	if res, _ := c.Get(ts.URL + "/static/app.css"); res.StatusCode != 200 {
		t.Fatal("static should be public")
	}
	if res, _ := c.Get(ts.URL + "/healthz"); res.StatusCode != 200 {
		t.Fatal("healthz should be public")
	}

	res = post(t, c, ts, "/login", url.Values{"password": {"nope"}}, false)
	if loc := res.Header.Get("Location"); loc != "/login?e=1" {
		t.Fatalf("wrong password: %s", loc)
	}
	res = post(t, c, ts, "/login", url.Values{"password": {"s3cret"}}, false)
	if res.Header.Get("Location") != "/" {
		t.Fatalf("login: %s", res.Header.Get("Location"))
	}
	res, _ = c.Get(ts.URL + "/queue")
	if b := body(t, res); res.StatusCode != 200 || !strings.Contains(b, "Nothing here yet") {
		t.Fatalf("queue after login: %d", res.StatusCode)
	}
	if res, _ := c.Get(ts.URL + "/"); res.StatusCode != 200 {
		t.Fatalf("search page: %d", res.StatusCode)
	}
	if res, _ := c.Get(ts.URL + "/settings"); res.StatusCode != 200 {
		t.Fatalf("settings page: %d", res.StatusCode)
	}
}

func TestCrossSitePostBlocked(t *testing.T) {
	ts, _ := newTestServer(t, "")
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/jobs/clear", nil)
	req.Header.Set("Origin", "https://evil.example")
	res, _ := http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site POST: %d", res.StatusCode)
	}
}

func TestSettingsSaveKeepsAPIKey(t *testing.T) {
	ts, s := newTestServer(t, "")
	defer ts.Close()
	c := client()
	form := url.Values{
		"paheDomain": {"https://animepahe.pw/"}, "flareSolverrUrl": {"http://fs:8191"}, "outputDir": {"/DATA/.manual-import"},
		"sonarrUrl": {"http://sonarr:8989"}, "sonarrApiKey": {"abc"}, "defaultQuality": {"720"}, "defaultAudio": {"eng"},
		"concurrency": {"2"}, "delaySeconds": {"3"},
	}
	res := post(t, c, ts, "/settings", form, false)
	if res.Header.Get("Location") != "/settings?saved=1" {
		t.Fatalf("save: %d %s", res.StatusCode, body(t, res))
	}
	got := s.Settings.Get()
	if got.PaheDomain != "animepahe.pw" || got.SonarrAPIKey != "abc" || got.DefaultQuality != 720 || got.DefaultAudio != "eng" || got.DefaultAutoImport {
		t.Fatalf("saved: %+v", got)
	}
	form.Del("sonarrApiKey")
	post(t, c, ts, "/settings", form, false)
	if s.Settings.Get().SonarrAPIKey != "abc" {
		t.Fatal("omitted API key should keep the saved one")
	}
	res, _ = c.Get(ts.URL + "/settings")
	if b := body(t, res); strings.Contains(b, "abc") || !strings.Contains(b, `value="***"`) {
		t.Fatal("settings page must not echo the API key")
	}
	form.Set("sonarrApiKey", "new-key")
	post(t, c, ts, "/settings", form, false)
	if s.Settings.Get().SonarrAPIKey != "new-key" {
		t.Fatal("replace key")
	}
	form.Set("sonarrApiKey", "")
	post(t, c, ts, "/settings", form, false)
	if s.Settings.Get().SonarrAPIKey != "" {
		t.Fatal("clear key")
	}
	form.Set("outputDir", "relative/path")
	res = post(t, c, ts, "/settings", form, false)
	if b := body(t, res); !strings.Contains(b, "absolute path") {
		t.Fatal("relative output dir should be rejected")
	}
}

func TestOutputTest(t *testing.T) {
	ts, _ := newTestServer(t, "")
	defer ts.Close()
	dir := t.TempDir() + "/staging"
	res := post(t, client(), ts, "/settings/test/output", url.Values{"outputDir": {dir}}, true)
	if b := body(t, res); !strings.Contains(b, "alert ok") || !strings.Contains(b, "Writable") {
		t.Fatalf("output test: %s", b)
	}
	res = post(t, client(), ts, "/settings/test/output", url.Values{"outputDir": {"nope"}}, true)
	if b := body(t, res); !strings.Contains(b, "alert bad") {
		t.Fatalf("relative path should fail: %s", b)
	}
	res = post(t, client(), ts, "/settings/test/pahe", url.Values{"paheDomain": {""}}, true)
	if b := body(t, res); !strings.Contains(b, "Enter the animepahe URL") {
		t.Fatalf("pahe test without domain: %s", b)
	}
}

func TestEnqueue(t *testing.T) {
	ts, s := newTestServer(t, "")
	defer ts.Close()
	c := client()
	res := post(t, c, ts, "/queue", url.Values{"session": {"a1"}, "title": {"Death Note"}}, true)
	if b := body(t, res); !strings.Contains(b, "Select at least one episode") || res.Header.Get("HX-Retarget") != "#form-msg" {
		t.Fatalf("no episodes: %s", b)
	}
	form := url.Values{
		"session": {"a1"}, "title": {"Death Note"}, "quality": {"720"}, "audio": {"jpn"},
		"ep": {"1|s1|01", "2|s2|02"}, "import": {"on"},
	}
	res = post(t, c, ts, "/queue", form, true)
	if b := body(t, res); !strings.Contains(b, "Choose the Sonarr series") {
		t.Fatalf("import without series: %s", b)
	}
	form.Del("import")
	res = post(t, c, ts, "/queue", form, true)
	if res.Header.Get("HX-Redirect") != "/queue?added=2" {
		t.Fatalf("enqueue redirect: %q", res.Header.Get("HX-Redirect"))
	}
	js := s.Jobs.List()
	if len(js) != 2 || js[0].EpisodeLabel != "01" || js[0].Quality != 720 || js[0].Import {
		t.Fatalf("jobs: %+v", js)
	}
	res, _ = c.Get(ts.URL + "/queue/list")
	if b := body(t, res); !strings.Contains(b, "episode 1") || !strings.Contains(b, "Cancel") {
		t.Fatalf("job list: %s", b)
	}
	res = post(t, c, ts, "/jobs/"+js[1].ID+"/cancel", nil, true)
	if b := body(t, res); !strings.Contains(b, "canceled") || !strings.Contains(b, "Retry") {
		t.Fatalf("cancel: %s", b)
	}
}
