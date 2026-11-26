# MKissa deployment feasibility

On 2026-11-25, the deployment image built from `b3b5cbf` (Linux arm64) received HTTP 403 for Death Note episode 1 sub. Discovery stopped at the watch page. The response alone does not identify the cause of the denial.

Run from the repository root with Docker Compose. The GET uses the image’s BusyBox wget and discards the response body.

```sh
PAHE_PASSWORD=probe docker compose run --rm --no-deps --entrypoint wget pahe-arr \
  -S -O /dev/null -T 12 https://mkissa.to/anime/RezHft5pjutwWcE3B/p-1-sub
```

Observed output (exit status 1):

```text
Connecting to mkissa.to (172.67.187.90:443)
  HTTP/1.1 403 Forbidden
wget: server returned error: HTTP/1.1 403 Forbidden
```

Earlier host testing started with a manually supplied Ok player URL and produced a 240p MP4 lasting 22:57 with audio and visible English subtitles in sampled frames. That run skipped player discovery. The container run produced no player URL or MP4, leaving duration, audio sync and subtitle visibility unverified for the saved-reference workflow. Issue #13 remains blocked at discovery.
