# pahe-arr

A lightweight animepahe downloader with a web UI for a CasaOS / *arr homelab. Search animepahe,
pick episodes, download them, and hand them to Sonarr, which renames them into your library
and tells Jellyfin.

- One static Go binary + ffmpeg on Alpine
- No browser: FlareSolverr passes Cloudflare; kwik and HLS are handled in Go
- Queue survives restarts; one episode at a time, 2 automatic retries, cancel and retry
- Settings in the web UI, each with a **Test** button

## Install

### CasaOS dashboard (published releases)

After the first release is published, download `casaos.yml` from
[GitHub Releases](https://github.com/barestu/pahe-arr/releases). In CasaOS, open
**App Store → Custom Install → Import**, then paste or upload that file.
Set `PAHE_PASSWORD` and choose your host config and download folders before
installing. The suggested `/DATA/AppData/pahe-arr/...` folders are editable CasaOS
defaults, not required locations. FlareSolverr is included on the private Compose
network. Open port 8585, then set and test the animepahe domain in Settings.

Use the release attachment to install an exact image version. The repository's
`deploy/casaos.yml` tracks `latest` and is a template for future releases; it will
not install until a public image exists. If your DNS redirects animepahe to a block
page, configure working DNS for both services. Configure Sonarr separately if used.

For upgrades, back up config and change the image version in CasaOS. The pipeline
publishes images but never automatically updates your server.
See [CONTRIBUTING.md](CONTRIBUTING.md) for Git rules and the release procedure.

### Build from source

On the server:

```bash
unzip -o pahe-arr.zip && cd pahe-arr
cp .env.server.example .env
# Edit .env and set PAHE_PASSWORD before starting.
sudo docker compose up -d --build
```

Open `http://<server-ip>:8585`, log in, go to **Settings**, press **Test** on each section, then **Save**.

By default, settings and the queue live in `./data/config`, and downloads in
`./data/downloads`, relative to the repository. Set `PAHE_HOST_CONFIG_DIR` and
`PAHE_HOST_DOWNLOAD_DIR` in `.env` to use your own server paths. Set `PUID`/`PGID`
to the user and group that should own these files (defaults: `1000:1000`).
Inside the container, the download folder is `/config/downloads`.

Run FlareSolverr separately and enter its reachable URL and the current animepahe
domain in web Settings. No server address or media-library location is assumed.
If using Sonarr, configure its URL, API key, and the download path as Sonarr sees it.
Existing saved settings take precedence over first-run environment values; update
the output folder in Settings when migrating an existing installation.

### Updating

```bash
unzip -o pahe-arr.zip && cd pahe-arr
sudo docker compose up -d --build
sudo docker image prune -f
```

`.env` isn't in the zip, so your password survives updates.

## Local development

With Go 1.22 or newer, copy `.env.example` to `.env`, then run from the repository root
on Windows, macOS, or Linux:

```sh
go run ./cmd/pahe serve
```

The app reads `.env` automatically and starts at `http://127.0.0.1:8585`.
Settings and queue data go in `tmp/config`, and downloads go in `tmp/downloads`
inside this project. Both `tmp/` and `.env` are ignored by Git. Change the
download folder in the web UI after the first run; saved settings take precedence.
Existing environment variables override `.env` values. The file supports comments
and quoted values. Without `.env`, the app keeps its production defaults.
For Docker deployment, add `PAHE_PASSWORD` to `.env` as described above.

If Make is installed, `make run` runs the same command.

- `make` or `make check` runs `go vet ./...` followed by `go test ./...`.
- `make test` runs the full test suite.
- To run a single test, use `go test ./internal/hls -run TestName`.

### Local Docker stack

With Docker running Linux containers, start the app and FlareSolverr on Windows,
macOS, or Linux:

```sh
docker compose -f docker-compose.local.yml up -d --build
```

Open `http://localhost:8585`. In **Settings**, enter the current animepahe domain,
use the **Test** buttons, then **Save**. FlareSolverr is preconfigured as
`http://flaresolverr:8191`; the app waits for it to be healthy before starting.
Sonarr is optional; turn off **Send to Sonarr** when testing downloads alone.
The local UI has no login unless `PAHE_PASSWORD` is set in `.env`.

Config and queue data live in `tmp/config`, and completed videos go in
`tmp/downloads` (shown as `/config/downloads` inside the container).
These host folders match `.env.example`. When switching between Docker and native runs, update the saved output folder and FlareSolverr URL in Settings to match that environment.
Both published ports are bound to localhost. Container paths and the internal
FlareSolverr URL override the native paths and URL from `.env`.

To run the app with Go while keeping only FlareSolverr in Docker:

```sh
docker compose -f docker-compose.local.yml up -d flaresolverr
go run ./cmd/pahe serve
```

Use `http://127.0.0.1:8191` for FlareSolverr in native app Settings (also seeded by
`.env.example`). Saved settings take precedence over first-run defaults.
Stop any app using port 8585 before switching between native and Docker runs.

Inspect or stop the local stack:

```sh
docker compose -f docker-compose.local.yml logs -f
docker compose -f docker-compose.local.yml down
```

Stopping the stack preserves the files under `tmp`.

## Using it

1. **Search** for a title and open it.
2. **Pick episodes**: type a range like `1-12, 15` and press Select, use All/None, or Shift-click to select a run.
3. **Options** (chosen per queue; defaults come from Settings):
   - **Quality**: if an episode doesn't have it, the closest one is used.
   - **Audio**: Japanese (sub) or English (dub).
   - **Send to Sonarr**: choose the series (best title matches are listed first) and how episode numbers map:

     | Sonarr series | Numbering | Offset |
     |---|---|---|
     | Anime, numbered straight through | Absolute | 0 |
     | animepahe lists a sequel as its own show starting at 1, Sonarr continues at 26 | Absolute | 25 |
     | Regular (non-anime) series | Season N | 0 |
4. **Queue** shows live progress. Failed jobs keep their error and can be retried. If only the
   Sonarr import failed, Retry skips the download and only re-imports.

Without Sonarr, files stay in the output folder as `[AnimePahe] Title - 01 [1080p].mp4`, ready for
Sonarr's **Wanted → Manual Import** (mode Move).

## Settings

| Setting | Notes |
|---|---|
| animepahe URL | The domain your browser uses (e.g. `animepahe.pw`). It moves occasionally; Test tells you if it's live |
| FlareSolverr URL | e.g. `http://192.168.100.11:8191`. Byparr works too |
| Output folder | Path inside the container. Keep it on the same disk as the library so Sonarr's move is instant |
| Sonarr URL / API key | API key from Sonarr → Settings → General. It's never shown again after saving |
| Output folder as Sonarr sees it | Only if Sonarr mounts the disk at a different path |
| Download defaults | Pre-selected on each anime page |

## Command line

The CLI uses the settings saved in the web UI:

```bash
sudo docker compose exec -u 1000:1000 pahe-arr pahe doctor
sudo docker compose exec -u 1000:1000 pahe-arr pahe search "death note"
```

`doctor` checks every step (FlareSolverr, API, episode list, play page, kwik, stream decryption) and says which one fails.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| animepahe Test: "no Cloudflare clearance" | Not the live domain. Open animepahe in your browser and copy the domain |
| animepahe Test: Cloudflare still blocks | FlareSolverr can't pass the current challenge; try Byparr |
| Download fails at the stream | CDN rejected the request; `doctor` shows which host |
| "Sonarr does not see …" | Sonarr mounts the disk at another path: set "Output folder as Sonarr sees it" |
| "Sonarr has no episode for …" | Numbering is off: check Absolute vs Season and the offset |

## Notes

- Keep it on your LAN (or behind Tailscale) and set a password. Login uses a signed cookie and cross-site requests are blocked.
- Gentle on the CDN: max 4 parallel segments, backoff on HTTP 429, configurable pause between episodes.
- animepahe and HLS logic are ported from barestu/animepahe-browser-dl (MIT); htmx is bundled (0BSD). See `THIRD_PARTY_NOTICES.md`.
- For personal use. You're responsible for complying with the sites' terms and the copyright law where you live.

## Layout

```
cmd/pahe/            serve (web UI), doctor, search, episodes, download
internal/web/        htmx UI, auth, settings tests (templates + static embedded)
internal/jobs/       persistent queue and worker
internal/sonarr/     Sonarr v3 API: series, episodes, ManualImport
internal/config/     settings.json store
internal/grab/       one-episode download pipeline
internal/pahe/       animepahe search, episodes, play page
internal/kwik/       kwik page → m3u8 (pure-Go unpacker)
internal/hls/        HLS playlist, AES-128, segments
internal/cf/         FlareSolverr client
internal/session/    Cloudflare-aware HTTP session
internal/dl/         ffmpeg remux (local only)
```
