# AGENTS.md

Go web app + CLI (`cmd/pahe`) that downloads animepahe episodes and hands them to Sonarr. Layout and user docs: `README.md`.

## Commands

- Run `make check` (the default for `make`) to run `go vet ./...` followed by `go test ./...`. Keep `gofmt` clean.
- Run the full test suite with `make test` or a single test with `go test ./internal/hls -run TestName`. Tests use `httptest` and `t.TempDir()` and require neither external services nor ffmpeg.
- Start the local web app with `make run` or `go run ./cmd/pahe serve`. Copy `.env.example` to `.env` first; it uses `./tmp/config` and `./tmp/downloads`.
- `doctor`, `search`, and `download` need a live animepahe domain and FlareSolverr. `download` also needs `ffmpeg` on PATH.
- Build and deploy on the server with `docker compose up -d --build`. Copy `.env.server.example` to `.env` and set `PAHE_PASSWORD`; host folders are configurable and default to `./data/config` and `./data/downloads`.
- Follow `CONTRIBUTING.md`: short-lived branches, PRs, green CI, squash merge. Publish only from validated version tags on main. Never create or move a release tag without an explicit release request.

## Constraints

- **Dependencies** are allowed. Add them with `go get` and commit both `go.mod` and `go.sum`. The Docker build uses `CGO_ENABLED=0`, so choose pure-Go packages (e.g. `modernc.org/sqlite`, not `mattn/go-sqlite3`). The package must also support Go 1.22 (see below). If `go get` raises the `go` directive above 1.22, the Docker build fails because the `golang` image sets `GOTOOLCHAIN=local`. Add each new dependency's license to `THIRD_PARTY_NOTICES.md`.
- **Go 1.22**: `go.mod` and the Dockerfile (`golang:1.22-alpine`) pin it, but the local toolchain may be newer. Don't use stdlib APIs or language features added after 1.22 (e.g. range-over-func, `iter`, `maps.Keys`); they compile locally and break the Docker build.
- `.dockerignore` excludes `*.md`; don't rely on markdown files at runtime.

## Architecture notes

- Flow: `web` / CLI → `jobs` (queue worker) → `grab.Episode` (play page → `kwik` → `hls` download → `dl` ffmpeg remux) → `sonarr` ManualImport.
- `pahe` talks to animepahe through `session` + `cf` (FlareSolverr provides Cloudflare cookies/UA). `kwik` resolves the m3u8 with plain HTTP, no browser.
- State lives in `$PAHE_CONFIG_DIR`: `settings.json`, `jobs.json`, and a cookie-signing secret. Write via `config.WriteJSONAtomic`. Env vars (`PAHE_DOMAIN`, `FLARESOLVERR_URL`, `SONARR_*`, ...) only seed the first run; after that saved settings win, including for the CLI.
- Jobs: one runs at a time, `MaxAutoRetries = 2`, and active jobs reset to `queued` on restart. If only the Sonarr import failed, Retry skips the download.
- Downloads go to a hidden `.<name>.ts.part` file and the `.mp4` appears only when complete, so Sonarr never sees partial files. Keep the filename format `[AnimePahe] Title - 01 [1080p][ [Dub]].mp4`: Sonarr parses it as absolute numbering.
- CDN etiquette: segment concurrency is clamped to 1–4 and there's backoff on 429. Don't raise these limits.

## Web UI

- Templates and static files are `go:embed`ed (`internal/web/templates`, `internal/web/static`), so rebuild after editing them.
- Each page is parsed as `base.html` + `partials.html` + `<page>.html`. A new page must be added to the list in `Server.parse()` (`internal/web/server.go`).
- htmx is vendored (`static/htmx.min.js`). Strict CSP (`script-src 'self'; style-src 'self'`) means no inline `<script>`, `style=` attributes, or CDN assets: put JS in `app.js` and CSS in `app.css`.
- Every POST must pass the same-origin check (`Origin`/`Sec-Fetch-Site`). Unauthed htmx requests get `HX-Redirect: /login`.
- Uses Go 1.22 `ServeMux` method/wildcard patterns (`"POST /jobs/{id}/{action}"`).

## Licensing

`internal/pahe` and `internal/hls` are ported from barestu/animepahe-browser-dl (MIT). Keep `THIRD_PARTY_NOTICES.md` in sync if you add ported code.
