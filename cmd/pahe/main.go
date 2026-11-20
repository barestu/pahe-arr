// Command pahe is the spike CLI: it checks whether animepahe can be reached and downloaded
// from this server without a browser, and downloads episodes into a staging folder.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"pahe-arr/internal/cf"
	appconfig "pahe-arr/internal/config"
	"pahe-arr/internal/grab"
	"pahe-arr/internal/hls"
	"pahe-arr/internal/jobs"
	"pahe-arr/internal/kwik"
	"pahe-arr/internal/pahe"
	"pahe-arr/internal/session"
	"pahe-arr/internal/web"
)

const usageText = `pahe — animepahe downloader (spike)

Usage:
  pahe serve                          Run the web UI (default in the container)
  pahe doctor [--query naruto]        Test every step without downloading
  pahe search <title>                 Find an anime and its session id
  pahe episodes <anime-session>       List episodes
  pahe download <anime-session> [flags]
      --eps 1-12,15     episodes to fetch (default: all)
      --quality 1080    target height; nearest available is used
      --audio jpn       jpn (sub) or eng (dub)
      --concurrency 3   parallel segment downloads (1-4)
      --out /DATA/.manual-import
      --title "Name"    override the folder/file title

Environment:
  PAHE_PASSWORD      web UI password (empty = no login)
  PAHE_CONFIG_DIR    settings and queue (default /config)
  PAHE_LISTEN        web UI address (default :8585)
  PAHE_DOMAIN        animepahe domain (CLI; otherwise taken from saved settings)
  FLARESOLVERR_URL   e.g. http://192.168.100.11:8191 (CLI; otherwise from settings)
  PAHE_DELAY         pause between requests (default 1s)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "serve":
		err = runServe(ctx)
	case "doctor":
		err = runDoctor(ctx, args)
	case "search":
		err = runSearch(ctx, args)
	case "episodes":
		err = runEpisodes(ctx, args)
	case "download":
		err = runDownload(ctx, args)
	case "help", "-h", "--help":
		fmt.Print(usageText)
	default:
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type config struct {
	host         string
	flaresolverr string
	delay        time.Duration
}

func loadConfig() (config, error) {
	// Settings saved in the web UI win; environment variables are the fallback.
	saved, found, _ := appconfig.Load(configDir())
	host, fs := saved.PaheDomain, saved.FlareSolverrURL
	if !found || host == "" {
		host = appconfig.NormalizeDomain(os.Getenv("PAHE_DOMAIN"))
	}
	if !found || fs == "" {
		fs = strings.TrimSpace(os.Getenv("FLARESOLVERR_URL"))
	}
	if host == "" {
		return config{}, errors.New("animepahe domain not set: save it in the web UI settings or set PAHE_DOMAIN")
	}
	if fs == "" {
		return config{}, errors.New("FlareSolverr URL not set: save it in the web UI settings or set FLARESOLVERR_URL")
	}
	delay := time.Second
	if v := os.Getenv("PAHE_DELAY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return config{}, fmt.Errorf("PAHE_DELAY: %w", err)
		}
		delay = d
	}
	return config{host: host, flaresolverr: fs, delay: delay}, nil
}

func configDir() string {
	if d := strings.TrimSpace(os.Getenv("PAHE_CONFIG_DIR")); d != "" {
		return d
	}
	return "/config"
}

func newClient(c config) *pahe.Client {
	return &pahe.Client{S: session.New(c.host, cf.New(c.flaresolverr)), Delay: c.delay}
}

var (
	plainHTTP = &http.Client{Timeout: 30 * time.Second}
	cdnHTTP   = &http.Client{Timeout: 2 * time.Minute}
)

// ---------- doctor ----------

func runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	query := fs.String("query", "naruto", "search term used for the test")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	c := newClient(cfg)

	fail := func(step, hint string, err error) error {
		fmt.Printf("FAIL\n      %v\n      → %s\n", err, hint)
		return fmt.Errorf("doctor stopped at: %s", step)
	}

	fmt.Printf("[1/6] FlareSolverr solves https://%s/ ... ", cfg.host)
	sol, err := c.S.Solve(ctx)
	if err != nil {
		return fail("FlareSolverr", "Check FLARESOLVERR_URL. If FlareSolverr is reachable but cannot solve, this site needs a stealth browser.", err)
	}
	fmt.Printf("PASS\n      cookies: %s\n      user agent: %s\n", strings.Join(sol.CookieNames(), ", "), truncate(sol.UserAgent, 70))

	fmt.Printf("[2/6] Plain HTTP to animepahe search API ... ")
	results, err := c.Search(ctx, *query)
	if err != nil {
		var ce *session.ChallengeError
		if errors.As(err, &ce) {
			return fail("animepahe API", "Cloudflare rejects Go's HTTP client even with a valid cookie. Next: a browser-like TLS client (utls), else a browser fallback.", err)
		}
		return fail("animepahe API", "Check PAHE_DOMAIN. If the response shape changed, the search parser needs updating.", err)
	}
	if len(results) == 0 {
		return fail("animepahe API", "Try another --query.", errors.New("no search results"))
	}
	a := results[0]
	fmt.Printf("PASS (%d results, testing %q)\n", len(results), a.Title)

	fmt.Printf("[3/6] Episode list ... ")
	eps, err := c.Episodes(ctx, a.Session)
	if err != nil || len(eps) == 0 {
		if err == nil {
			err = errors.New("no episodes returned")
		}
		return fail("episode list", "The release API may have changed.", err)
	}
	fmt.Printf("PASS (%d episodes)\n", len(eps))

	ep := eps[0]
	fmt.Printf("[4/6] Play page sources (episode %s) ... ", ep.Number)
	sources, page, err := c.PlayPage(ctx, a.Session, ep.Session)
	if err != nil {
		return fail("play page", "Request failed.", err)
	}
	if len(sources) == 0 {
		return fail("play page", "Markup changed or blocked: "+pahe.DescribePlayPage(page), errors.New("no sources parsed"))
	}
	fmt.Printf("PASS (%s)\n", describeSources(sources))

	src, _ := pahe.PickSource(sources, 720, "jpn")
	fmt.Printf("[5/6] kwik page → m3u8, no browser ... ")
	res, err := kwik.Resolve(ctx, plainHTTP, src.KwikURL, cfg.host, c.S.UserAgent())
	if err != nil {
		hint := "kwik's player script changed; the unpacker or regex needs updating."
		if errors.Is(err, kwik.ErrChallenge) {
			hint = "kwik itself is behind a challenge here. Needs a browser fallback for the kwik step."
		}
		return fail("kwik", hint, err)
	}
	fmt.Printf("PASS (stream host %s)\n", hostOf(res.M3U8))

	fmt.Printf("[6/6] Playlist, key and first segment (via %s) ... ", hostOf(res.Origin))
	f := &hls.Fetcher{Client: cdnHTTP, Header: res.Headers(c.S.UserAgent())}
	segs, err := hls.Probe(ctx, f, res.M3U8)
	if err != nil {
		return fail("stream", "The CDN rejected a request or the stream format changed.", err)
	}
	fmt.Printf("PASS (%d segments, decrypts to MPEG-TS)\n", segs)

	fmt.Println("\nVerdict: browserless works from this server. Try a real download next.")
	return nil
}

// ---------- serve ----------

func runServe(ctx context.Context) error {
	dir := configDir()
	settings, err := appconfig.Open(dir)
	if err != nil {
		return fmt.Errorf("config dir %s: %w", dir, err)
	}
	clients := jobs.NewClients()
	manager, err := jobs.NewManager(dir, settings, clients)
	if err != nil {
		return err
	}
	secret, err := web.LoadSecret(dir)
	if err != nil {
		return err
	}
	srv := &web.Server{Settings: settings, Jobs: manager, Clients: clients, Password: os.Getenv("PAHE_PASSWORD"), Secret: secret}
	h, err := srv.Handler()
	if err != nil {
		return err
	}
	addr := os.Getenv("PAHE_LISTEN")
	if addr == "" {
		addr = ":8585"
	}
	hs := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}

	go manager.Run(ctx)
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(shut)
	}()
	if srv.Password == "" {
		fmt.Println("warning: PAHE_PASSWORD is empty, the web UI has no login")
	}
	fmt.Printf("pahe-arr listening on %s (config %s)\n", addr, dir)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ---------- search / episodes ----------

func runSearch(ctx context.Context, args []string) error {
	q := strings.TrimSpace(strings.Join(args, " "))
	if q == "" {
		return errors.New("usage: pahe search <title>")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	results, err := newClient(cfg).Search(ctx, q)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tTYPE\tEPS\tYEAR\tSTATUS\tTITLE")
	for _, a := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", a.Session, a.Type, a.Episodes, a.Year, a.Status, a.Title)
	}
	return tw.Flush()
}

func runEpisodes(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: pahe episodes <anime-session>")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	eps, err := newClient(cfg).Episodes(ctx, args[0])
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "EPISODE\tSESSION")
	for _, e := range eps {
		fmt.Fprintf(tw, "%s\t%s\n", e.Number, e.Session)
	}
	return tw.Flush()
}

// ---------- download ----------

func runDownload(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	epsSpec := fs.String("eps", "all", "episodes, e.g. 1-12,15")
	quality := fs.Int("quality", 1080, "target height")
	audio := fs.String("audio", "jpn", "jpn or eng")
	out := fs.String("out", "/DATA/.manual-import", "output folder")
	titleFlag := fs.String("title", "", "override title")
	concurrency := fs.Int("concurrency", 3, "parallel segment downloads (1-4)")

	var animeSession string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		animeSession, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if animeSession == "" && fs.NArg() > 0 {
		animeSession = fs.Arg(0)
	}
	if animeSession == "" {
		return errors.New("usage: pahe download <anime-session> [flags]")
	}
	if *audio != "jpn" && *audio != "eng" {
		return errors.New("--audio must be jpn or eng")
	}
	selected, err := parseSelection(*epsSpec)
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	c := newClient(cfg)

	title := *titleFlag
	if title == "" {
		if title, err = c.AnimeTitle(ctx, animeSession); err != nil {
			return err
		}
	}
	all, err := c.Episodes(ctx, animeSession)
	if err != nil {
		return err
	}
	var eps []pahe.Episode
	for _, e := range all {
		if selected(e.Number) {
			eps = append(eps, e)
		}
	}
	if len(eps) == 0 {
		return fmt.Errorf("no episodes match %q (%d available)", *epsSpec, len(all))
	}
	width := grab.LabelWidth(all)
	folder := grab.SafeName(title)
	fmt.Printf("%s: %d episode(s) → %s\n", title, len(eps), filepath.Join(*out, folder))

	var failed []string
	for i, ep := range eps {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(cfg.delay):
			}
		}
		fmt.Printf("  episode %s\n", ep.Number)
		res, err := grab.Episode(ctx, c, ep, grab.Options{
			OutputDir: *out, Title: title, Label: grab.Label(ep.Number, width),
			Quality: *quality, Audio: *audio, Concurrency: *concurrency,
		}, func(stage grab.Stage, done, total int) {
			if stage == grab.Downloading {
				fmt.Fprintf(os.Stderr, "\r      segments %d/%d (%d%%)", done, total, done*100/total)
			}
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintf(os.Stderr, "\n      failed: %v\n", err)
			failed = append(failed, ep.Number)
			continue
		}
		if res.Skipped {
			fmt.Printf("      already exists: %s\n", filepath.Base(res.Path))
			continue
		}
		fmt.Fprintln(os.Stderr)
		fmt.Printf("      %dp %s via %s → %s\n", res.Quality, res.Audio, res.Via, filepath.Base(res.Path))
	}
	fmt.Printf("Done: %d ok, %d failed\n", len(eps)-len(failed), len(failed))
	if len(failed) > 0 {
		return fmt.Errorf("failed episodes: %s", strings.Join(failed, ", "))
	}
	return nil
}

// ---------- helpers ----------

// parseSelection accepts "all" or a list like "1-12,15" (decimals such as 12.5 work too).
func parseSelection(spec string) (func(string) bool, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.EqualFold(spec, "all") {
		return func(string) bool { return true }, nil
	}
	type rng struct{ lo, hi float64 }
	var ranges []rng
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		loS, hiS, isRange := strings.Cut(part, "-")
		lo, err := strconv.ParseFloat(strings.TrimSpace(loS), 64)
		if err != nil {
			return nil, fmt.Errorf("bad episode selection %q", part)
		}
		hi := lo
		if isRange {
			if hi, err = strconv.ParseFloat(strings.TrimSpace(hiS), 64); err != nil || hi < lo {
				return nil, fmt.Errorf("bad episode range %q", part)
			}
		}
		ranges = append(ranges, rng{lo, hi})
	}
	if len(ranges) == 0 {
		return nil, fmt.Errorf("bad episode selection %q", spec)
	}
	return func(num string) bool {
		n, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return false
		}
		for _, r := range ranges {
			if n >= r.lo && n <= r.hi {
				return true
			}
		}
		return false
	}, nil
}

func describeSources(sources []pahe.Source) string {
	set := map[string]bool{}
	for _, s := range sources {
		set[fmt.Sprintf("%dp %s", s.Quality, s.Audio)] = true
	}
	var parts []string
	for k := range set {
		parts = append(parts, k)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "?"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
