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

### Grab an episode from Sonarr

In pahe-arr, open **Settings → Sonarr integration key → Generate key**. Copy the key into both Sonarr connections below. To replace it, click **Regenerate key** and update both entries in Sonarr. The new key takes effect immediately and survives restarts. Use the hostname and port that Sonarr can reach. Sonarr needs access to pahe-arr’s completed downloads for import. If the containers use different paths, configure a Remote Path Mapping in Sonarr for the pahe-arr host.

1. Under **Settings → Download Clients**, add **SABnzbd**. Name it **AnimePahe**, enter the pahe-arr host and port (default `8585`), set **URL Base** to `/sab` and **Category** to `pahe`, and enter the integration key. Test and save.
2. Under **Settings → Indexers**, add a **Newznab** indexer. Set its URL to `http://HOST:8585/newznab`, API path to `/api`, and enter the same key. Select **TV/Anime (5070)** for anime categories. Enable **Interactive Search** only. Under advanced options assign the **AnimePahe** download client to this indexer. If you already use other Usenet indexers, explicitly assign each its usual client before enabling the AnimePahe client. This keeps ordinary NZBs on their existing download path.
3. Open an episode of an **Anime** series with an absolute episode number. Run **Interactive Search**, choose an AnimePahe release by resolution and Sub/Dub, and click **Grab**. Follow it in **Activity**. Sonarr imports the completed MP4 through Completed Download Handling.

The indexer matches the series title and absolute episode number. Ambiguous titles, missing episodes and season packs yield no result. The server log records skipped episode matches. If the selected audio or resolution disappears, the download fails after the normal queue retries. The stream’s byte size is unknown at search time.
