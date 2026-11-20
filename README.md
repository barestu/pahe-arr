# pahe-arr

A lightweight animepahe downloader with a web UI for a CasaOS / *arr homelab. Search animepahe,
pick episodes, download them, and hand them to Sonarr, which renames them into your library
and tells Jellyfin.

- One static Go binary (standard library only) + ffmpeg on Alpine
- No browser: FlareSolverr passes Cloudflare; kwik and HLS are handled in Go
- Queue survives restarts; one episode at a time, 2 automatic retries, cancel and retry
- Settings in the web UI, each with a **Test** button

## Install

On the server:

```bash
unzip -o pahe-arr.zip && cd pahe-arr
echo "PAHE_PASSWORD=choose-a-password" > .env
sudo docker compose up -d --build
```

Open `http://<server-ip>:8585`, log in, go to **Settings**, press **Test** on each section, then **Save**.

Settings and the queue live in `/DATA/AppData/pahe-arr/config`. Check that the two volume paths in
`docker-compose.yml` match your server before the first start.

### Updating

```bash
unzip -o pahe-arr.zip && cd pahe-arr
sudo docker compose up -d --build
sudo docker image prune -f
```

`.env` isn't in the zip, so your password survives updates.

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
