// Package web serves the htmx UI: search, episode picker, queue and settings.
package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"pahe-arr/internal/buildinfo"
	"pahe-arr/internal/cf"
	"pahe-arr/internal/config"
	"pahe-arr/internal/grab"
	"pahe-arr/internal/jobs"
	"pahe-arr/internal/pahe"
	"pahe-arr/internal/session"
	"pahe-arr/internal/sonarr"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	Settings *config.Store
	Jobs     *jobs.Manager
	Clients  *jobs.Clients
	Password string // empty disables login
	Secret   []byte // signs session cookies

	pages map[string]*template.Template
}

const cookieName = "pahe_session"

// LoadSecret reads or creates the cookie-signing secret in the config directory.
func LoadSecret(dir string) ([]byte, error) {
	p := filepath.Join(dir, ".secret")
	if b, err := os.ReadFile(p); err == nil && len(b) >= 32 {
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, os.WriteFile(p, b, 0o600)
}

var funcs = template.FuncMap{
	"version": func() string { return buildinfo.Version },
	"statusClass": func(s jobs.Status) string {
		switch {
		case s == jobs.Done:
			return "ok"
		case s == jobs.Failed:
			return "bad"
		case s == jobs.Canceled:
			return "muted"
		case s.Active():
			return "busy"
		}
		return "idle"
	},
	"active":   func(s jobs.Status) bool { return s.Active() },
	"finished": func(s jobs.Status) bool { return s.Finished() },
	"since": func(t time.Time) string {
		d := time.Since(t)
		switch {
		case d < time.Minute:
			return "just now"
		case d < time.Hour:
			return fmt.Sprintf("%dm ago", int(d.Minutes()))
		case d < 48*time.Hour:
			return fmt.Sprintf("%dh ago", int(d.Hours()))
		}
		return t.Format("2 Jan")
	},
	"base": filepath.Base,
}

func (s *Server) parse() error {
	s.pages = map[string]*template.Template{}
	for _, page := range []string{"search", "anime", "queue", "settings", "login"} {
		t, err := template.New("base.html").Funcs(funcs).ParseFS(templateFS, "templates/base.html", "templates/partials.html", "templates/"+page+".html")
		if err != nil {
			return err
		}
		s.pages[page] = t
	}
	return nil
}

// Handler builds the HTTP routes.
func (s *Server) Handler() (http.Handler, error) {
	if err := s.parse(); err != nil {
		return nil, err
	}
	static, _ := fs.Sub(staticFS, "static")
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)

	mux.HandleFunc("GET /{$}", s.searchPage)
	mux.HandleFunc("GET /search", s.search)
	mux.HandleFunc("GET /anime/{session}", s.animePage)
	mux.HandleFunc("POST /queue", s.enqueue)
	mux.HandleFunc("GET /queue", s.queuePage)
	mux.HandleFunc("GET /queue/list", s.queueList)
	mux.HandleFunc("POST /jobs/{id}/{action}", s.jobAction)
	mux.HandleFunc("POST /jobs/clear", s.clearJobs)
	mux.HandleFunc("GET /settings", s.settingsPage)
	mux.HandleFunc("POST /settings", s.saveSettings)
	mux.HandleFunc("POST /settings/test/{target}", s.testSettings)
	return s.secure(mux), nil
}

// ---------- auth & security ----------

func (s *Server) sign(exp int64) string {
	key := hmac.New(sha256.New, s.Secret)
	key.Write([]byte(s.Password)) // changing the password invalidates old sessions
	mac := hmac.New(sha256.New, key.Sum(nil))
	fmt.Fprintf(mac, "%d", exp)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) authed(r *http.Request) bool {
	if s.Password == "" {
		return true
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	expS, sig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expS, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.sign(exp)))
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; frame-ancestors 'none'")

		if r.Method == http.MethodPost && !sameOrigin(r) {
			http.Error(w, "cross-site request blocked", http.StatusForbidden)
			return
		}
		open := strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/login" || r.URL.Path == "/healthz"
		if !open && !s.authed(r) {
			if r.Header.Get("HX-Request") == "true" {
				h.Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.Password == "" || s.authed(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", "Log in", "", map[string]any{"Error": r.URL.Query().Get("e") != ""})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte(s.Password)) != 1 {
		time.Sleep(700 * time.Millisecond)
		http.Redirect(w, r, "/login?e=1", http.StatusSeeOther)
		return
	}
	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: fmt.Sprintf("%d.%s", exp, s.sign(exp)), Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: time.Unix(exp, 0),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---------- rendering ----------

type pageData struct {
	Title       string
	Nav         string
	AuthEnabled bool
	Ready       bool // animepahe + FlareSolverr configured
	Data        any
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page, title, nav string, data any) {
	set := s.Settings.Get()
	pd := pageData{Title: title, Nav: nav, AuthEnabled: s.Password != "", Ready: set.PaheDomain != "" && set.FlareSolverrURL != "", Data: data}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages[page].ExecuteTemplate(w, "base", pd); err != nil {
		log.Printf("render %s: %v", page, err)
	}
}

func (s *Server) partial(w http.ResponseWriter, page, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages[page].ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s/%s: %v", page, name, err)
	}
}

func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

func friendly(err error) string {
	var ce *session.ChallengeError
	switch {
	case errors.As(err, &ce):
		return "Cloudflare blocked the request even after FlareSolverr solved it. Try again, or check FlareSolverr."
	case errors.Is(err, context.DeadlineExceeded):
		return "Timed out. Check that the service is reachable from the container."
	}
	return err.Error()
}

// ---------- search ----------

type searchData struct {
	Q        string
	Results  []pahe.Anime
	Error    string
	Searched bool
}

func (s *Server) searchPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "search", "Search", "search", searchData{})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	d := searchData{Q: q, Searched: q != ""}
	if q != "" {
		set := s.Settings.Get()
		if set.PaheDomain == "" || set.FlareSolverrURL == "" {
			d.Error = "Set the animepahe URL and FlareSolverr URL in Settings first."
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
			defer cancel()
			res, err := s.Clients.For(set).Search(ctx, q)
			if err != nil {
				d.Error = friendly(err)
			}
			d.Results = res
		}
	}
	if isHX(r) {
		s.partial(w, "search", "results", d)
		return
	}
	s.render(w, r, "search", "Search", "search", d)
}

// ---------- anime / episode picker ----------

type epRow struct {
	Number, Session, Label string
}

type animeData struct {
	Session  string
	Title    string
	Episodes []epRow
	Error    string

	Quality    int
	Audio      string
	Qualities  []int
	SonarrOn   bool
	SonarrErr  string
	Suggested  []sonarr.Series
	AllSeries  []sonarr.Series
	DefaultID  int
	DefaultAbs bool
	AutoImport bool
}

func (s *Server) animePage(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("session")
	set := s.Settings.Get()
	d := animeData{
		Session: session, Title: strings.TrimSpace(r.URL.Query().Get("t")),
		Quality: set.DefaultQuality, Audio: set.DefaultAudio,
		Qualities: []int{9999, 1080, 720, 480, 360},
		SonarrOn:  set.SonarrConfigured(), AutoImport: set.DefaultAutoImport, DefaultAbs: true,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	c := s.Clients.For(set)
	if d.Title == "" {
		t, err := c.AnimeTitle(ctx, session)
		if err != nil {
			d.Error = friendly(err)
		}
		d.Title = t
	}
	if d.Error == "" {
		eps, err := c.Episodes(ctx, session)
		if err != nil {
			d.Error = friendly(err)
		}
		width := grab.LabelWidth(eps)
		for _, e := range eps {
			d.Episodes = append(d.Episodes, epRow{Number: e.Number, Session: e.Session, Label: grab.Label(e.Number, width)})
		}
	}
	if d.SonarrOn {
		all, err := sonarr.New(set.SonarrURL, set.SonarrAPIKey).Series(ctx)
		if err != nil {
			d.SonarrErr = friendly(err)
		}
		d.AllSeries = all
		d.Suggested = sonarr.Suggest(all, d.Title, 5)
		if len(d.Suggested) > 0 {
			d.DefaultID = d.Suggested[0].ID
			d.DefaultAbs = d.Suggested[0].SeriesType == "anime"
		}
	}
	s.render(w, r, "anime", d.Title, "search", d)
}

func (s *Server) enqueue(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fail := func(msg string) {
		w.Header().Set("HX-Retarget", "#form-msg")
		s.partial(w, "anime", "msg", map[string]any{"Error": msg})
	}
	title := strings.TrimSpace(r.FormValue("title"))
	session := r.FormValue("session")
	if title == "" || session == "" {
		fail("Missing anime details; reload the page.")
		return
	}
	picked := r.Form["ep"]
	if len(picked) == 0 {
		fail("Select at least one episode.")
		return
	}
	quality, _ := strconv.Atoi(r.FormValue("quality"))
	audio := r.FormValue("audio")
	if audio != "eng" {
		audio = "jpn"
	}
	base := jobs.Job{AnimeSession: session, AnimeTitle: title, Quality: quality, Audio: audio}
	if r.FormValue("import") == "on" {
		id, _ := strconv.Atoi(r.FormValue("series"))
		if id <= 0 {
			fail("Choose the Sonarr series to import into, or untick “Send to Sonarr”.")
			return
		}
		base.Import, base.SonarrSeriesID = true, id
		set := s.Settings.Get()
		if all, err := sonarr.New(set.SonarrURL, set.SonarrAPIKey).Series(r.Context()); err == nil {
			for _, sr := range all {
				if sr.ID == id {
					base.SonarrSeriesTitle = sr.Title
				}
			}
		}
		if r.FormValue("numbering") == "season" {
			base.SonarrSeason, _ = strconv.Atoi(r.FormValue("season"))
			if base.SonarrSeason <= 0 {
				fail("Enter the Sonarr season number.")
				return
			}
		}
		base.SonarrOffset, _ = strconv.Atoi(r.FormValue("offset"))
	}
	var add []jobs.Job
	for _, v := range picked {
		parts := strings.SplitN(v, "|", 3)
		if len(parts) != 3 {
			continue
		}
		j := base
		j.EpisodeNumber, j.EpisodeSession, j.EpisodeLabel = parts[0], parts[1], parts[2]
		add = append(add, j)
	}
	n := s.Jobs.Add(add...)
	if isHX(r) {
		w.Header().Set("HX-Redirect", "/queue?added="+strconv.Itoa(n))
		return
	}
	http.Redirect(w, r, "/queue?added="+strconv.Itoa(n), http.StatusSeeOther)
}

// ---------- queue ----------

type queueData struct {
	Jobs  []jobs.Job
	Added string
	Busy  bool
}

func (s *Server) queueData() queueData {
	js := s.Jobs.List()
	busy := false
	for _, j := range js {
		if !j.Status.Finished() {
			busy = true
		}
	}
	return queueData{Jobs: js, Busy: busy}
}

func (s *Server) queuePage(w http.ResponseWriter, r *http.Request) {
	d := s.queueData()
	d.Added = r.URL.Query().Get("added")
	s.render(w, r, "queue", "Queue", "queue", d)
}

func (s *Server) queueList(w http.ResponseWriter, r *http.Request) {
	s.partial(w, "queue", "joblist", s.queueData())
}

func (s *Server) jobAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	switch r.PathValue("action") {
	case "cancel":
		s.Jobs.Cancel(id)
	case "retry":
		s.Jobs.Retry(id)
	case "remove":
		s.Jobs.Remove(id)
	default:
		http.NotFound(w, r)
		return
	}
	s.queueList(w, r)
}

func (s *Server) clearJobs(w http.ResponseWriter, r *http.Request) {
	s.Jobs.ClearFinished()
	s.queueList(w, r)
}

// ---------- settings ----------

type settingsData struct {
	S         config.Settings
	HasAPIKey bool
	Saved     bool
	Error     string
	Qualities []int
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	set := s.Settings.Get()
	s.render(w, r, "settings", "Settings", "settings", settingsData{
		S: set, HasAPIKey: set.SonarrAPIKey != "", Saved: r.URL.Query().Get("saved") != "",
		Qualities: []int{9999, 1080, 720, 480, 360},
	})
}

// formSettings reads the settings form; a blank API key keeps the saved one.
func (s *Server) formSettings(r *http.Request) config.Settings {
	cur := s.Settings.Get()
	n := func(k string, def int) int {
		if v, err := strconv.Atoi(strings.TrimSpace(r.FormValue(k))); err == nil {
			return v
		}
		return def
	}
	next := config.Settings{
		PaheDomain:        r.FormValue("paheDomain"),
		FlareSolverrURL:   r.FormValue("flareSolverrUrl"),
		OutputDir:         r.FormValue("outputDir"),
		SonarrURL:         r.FormValue("sonarrUrl"),
		SonarrAPIKey:      r.FormValue("sonarrApiKey"),
		SonarrOutputDir:   r.FormValue("sonarrOutputDir"),
		DefaultQuality:    n("defaultQuality", cur.DefaultQuality),
		DefaultAudio:      r.FormValue("defaultAudio"),
		DefaultAutoImport: r.FormValue("defaultAutoImport") == "on",
		Concurrency:       n("concurrency", cur.Concurrency),
		DelaySeconds:      n("delaySeconds", cur.DelaySeconds),
	}
	if strings.TrimSpace(next.SonarrAPIKey) == "" {
		next.SonarrAPIKey = cur.SonarrAPIKey
	}
	if r.FormValue("clearApiKey") == "on" {
		next.SonarrAPIKey = ""
	}
	return next.Normalize()
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	next := s.formSettings(r)
	if next.OutputDir == "." || !filepath.IsAbs(next.OutputDir) {
		set := s.Settings.Get()
		s.render(w, r, "settings", "Settings", "settings", settingsData{S: next, HasAPIKey: set.SonarrAPIKey != "", Error: "Output folder must be an absolute path, e.g. /DATA/.manual-import", Qualities: []int{9999, 1080, 720, 480, 360}})
		return
	}
	prev := s.Settings.Get()
	if err := s.Settings.Set(next); err != nil {
		http.Error(w, "saving settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if prev.PaheDomain != next.PaheDomain || prev.FlareSolverrURL != next.FlareSolverrURL {
		s.Clients.Reset()
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

type testResult struct {
	OK      bool
	Message string
}

func (s *Server) testSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	set := s.formSettings(r)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	var res testResult
	switch r.PathValue("target") {
	case "pahe":
		res = testPahe(ctx, set)
	case "flaresolverr":
		res = testFlareSolverr(ctx, set)
	case "output":
		res = testOutput(set.OutputDir)
	case "sonarr":
		res = testSonarr(ctx, set)
	default:
		http.NotFound(w, r)
		return
	}
	s.partial(w, "settings", "testresult", res)
}

func testPahe(ctx context.Context, set config.Settings) testResult {
	if set.PaheDomain == "" {
		return testResult{Message: "Enter the animepahe URL."}
	}
	if set.FlareSolverrURL == "" {
		return testResult{Message: "animepahe needs FlareSolverr: enter its URL below first."}
	}
	sess := session.New(set.PaheDomain, cf.New(set.FlareSolverrURL))
	start := time.Now()
	sol, err := sess.Solve(ctx)
	if err != nil {
		return testResult{Message: friendly(err)}
	}
	hasClearance := false
	for _, n := range sol.CookieNames() {
		if n == "cf_clearance" {
			hasClearance = true
		}
	}
	c := &pahe.Client{S: sess}
	results, err := c.Search(ctx, "naruto")
	if err != nil {
		hint := ""
		if !hasClearance {
			hint = " No Cloudflare clearance was issued: this is probably not the live animepahe domain."
		}
		return testResult{Message: friendly(err) + "." + hint}
	}
	return testResult{OK: true, Message: fmt.Sprintf("Connected in %.0fs. Cloudflare passed, search returned %d results.", time.Since(start).Seconds(), len(results))}
}

func testFlareSolverr(ctx context.Context, set config.Settings) testResult {
	if set.FlareSolverrURL == "" {
		return testResult{Message: "Enter the FlareSolverr URL, e.g. http://192.168.100.11:8191"}
	}
	target := "https://www.google.com/"
	if set.PaheDomain != "" {
		target = "https://" + set.PaheDomain + "/"
	}
	start := time.Now()
	if _, err := cf.New(set.FlareSolverrURL).Solve(ctx, target); err != nil {
		return testResult{Message: friendly(err)}
	}
	return testResult{OK: true, Message: fmt.Sprintf("FlareSolverr answered and loaded %s in %.0fs.", strings.TrimSuffix(strings.TrimPrefix(target, "https://"), "/"), time.Since(start).Seconds())}
}

func testOutput(dir string) testResult {
	if dir == "" || dir == "." || !filepath.IsAbs(dir) {
		return testResult{Message: "Use an absolute path inside the container, e.g. /DATA/.manual-import"}
	}
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return testResult{Message: "Can't create the folder: " + err.Error()}
	}
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return testResult{Message: "Folder is not writable: " + err.Error()}
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	msg := "Writable."
	if free, ok := freeSpace(dir); ok {
		msg += " " + humanBytes(free) + " free."
	}
	return testResult{OK: true, Message: msg}
}

func testSonarr(ctx context.Context, set config.Settings) testResult {
	if set.SonarrURL == "" || set.SonarrAPIKey == "" {
		return testResult{Message: "Enter the Sonarr URL and API key (Sonarr → Settings → General)."}
	}
	cl := sonarr.New(set.SonarrURL, set.SonarrAPIKey)
	st, err := cl.Status(ctx)
	if err != nil {
		return testResult{Message: friendly(err)}
	}
	series, err := cl.Series(ctx)
	if err != nil {
		return testResult{Message: friendly(err)}
	}
	return testResult{OK: true, Message: fmt.Sprintf("%s %s connected, %d series in the library.", orDefault(st.AppName, "Sonarr"), st.Version, len(series))}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
