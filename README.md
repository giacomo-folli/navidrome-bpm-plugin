# Navidrome BPM Plugin

Community companion service for Navidrome that analyzes local music files, caches BPM results, and synchronizes Navidrome playlists named by BPM bucket such as `060bpm`, `120bpm`, and `220bpm`.

The scanner intentionally uses paginated `getAlbumList2` plus `getAlbum` calls. It does not use `getIndexes`, because Navidrome returns artists from that endpoint, not a complete song list.

## Features

- Connects to Navidrome through the OpenSubsonic API.
- Enumerates large libraries with album pagination.
- Detects BPM with Essentia, with aubio available as an alternate backend.
- Caches results in SQLite by file path and modification time.
- Synchronizes BPM bucket playlists idempotently.
- Optionally writes BPM metadata with FFmpeg.
- Runs once or continuously on a configurable interval.

## Configuration

Copy `config/config.example.yaml` to `config/config.yaml` and edit it:

```yaml
navidrome:
  url: http://localhost:4533
  username: admin
  password: password

musicDir: /music

analysis:
  detector: essentia
  workers: 6

playlist:
  bucketSize: 10
  minimum: 60
  maximum: 220

scan:
  interval: 30m

metadata:
  writeTags: false
```

Environment variables use the `NBDPM_` prefix, for example `NBDPM_NAVIDROME_URL`.

## Run

```bash
go run ./cmd/navidrome-bpm-plugin
```

For a single run, set `scan.interval` to `0s`.

## Docker

```bash
docker compose up --build
```

Mount `/music` to the same library root Navidrome uses, and mount `/config` for `config.yaml` and the SQLite cache.
