# pahe-arr

Search animepahe, queue episode downloads, and optionally import them into Sonarr from a web UI.

## Install

For a published version, download `compose.release.yml`, set `PAHE_PASSWORD` in the file, and run the command below. [GitHub Releases](https://github.com/barestu/pahe-arr/releases)

```sh
docker compose -f compose.release.yml up -d
```

If you need FlareSolverr, download `compose.flaresolverr.yml` from the same release and add `-f compose.flaresolverr.yml` before `up`. The overlay sets the FlareSolverr URL to `http://flaresolverr:8191`.

For CasaOS, import `compose.release.yml` via **App Store → Custom Install → Import**. Set your password and choose host folders for the config and downloads.

To build from source instead, copy `.env.server.example` to `.env`, set `PAHE_PASSWORD`, and run `docker compose up -d --build` from the project root.

Both Docker installs default to `./data/config` and `./data/downloads` on the host. To upgrade, back up the config and keep the same host folders. Use the Compose file from the newer release or rebuild from updated source.

## Configure

1. Open `http://<server-ip>:8585` and log in.
2. In **Settings**, enter the animepahe domain and your FlareSolverr URL. The default Docker **Output folder** is `/config/downloads`. Use the **Test** buttons, then **Save**.
3. For automatic import, add your Sonarr URL and the API key from Sonarr's **Settings → General**. If Sonarr sees the download folder at a different path, fill in **Output folder as Sonarr sees it**. Click **Test Sonarr**, then **Save**.

## Download episodes

1. Search for a title and open it.
2. Click episodes to toggle them. To select a range, click the first episode, then Shift-click the last. Use **All** to select every episode or **None** to clear the selection.
3. Choose the quality and audio. For automatic import, enable **Send to Sonarr after download** and select the series. Use **Absolute** for continuous numbering or **Season** and a season number for season-based numbering. If animepahe episode 1 maps to Sonarr episode 26, set **Offset** to `25`.
4. Click **Queue** under the download options. Open **Queue** in the navigation to follow progress or retry a failed job.

With automatic import off, downloads stay in your output folder. In Sonarr, use **Wanted → Manual Import** to import them later.

## Troubleshooting

- If search fails, test the animepahe domain and FlareSolverr URL in **Settings**.
- If Sonarr can't find the file, check **Output folder as Sonarr sees it**. If it can't find the episode, check the numbering and offset.

See the [contributor guide](CONTRIBUTING.md) for local development, tests, and CLI diagnostics.
