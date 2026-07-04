# Navidrome BPM Plugin

A native WebAssembly plugin for Navidrome that analyzes local music files, detects their BPM using a pure-Go implementation, and categorizes them into playlists.

## Features

- **Native Navidrome Plugin**: Runs entirely within Navidrome's secure WebAssembly sandbox (Extism). No external daemons, docker containers, or API polling required.
- **Pure-Go BPM Detection**: Uses `go-mp3` and `benjojo/bpm` to decode and analyze MP3s in-memory without relying on host system binaries like `aubio`.
- **BPM Playlists**: Analyzed songs are grouped into auto-created "BPM 120-129" style playlists via the Subsonic API (the sandbox filesystem is read-only, so tags can't be written).
- **Zero Configuration Setup**: Configure the scan interval directly from the Navidrome Web UI.
- **Efficient Caching**: Remembers analyzed files using Navidrome's native `KVStore` host service.
- **Manual Trigger**: Set the "Trigger Scan Now" config field to any new value to start a scan on demand.

## Triggering a Scan Manually

Navidrome has no way to invoke a plugin directly, so the plugin polls its own
configuration once a minute. To start a scan without waiting for the schedule:

1. Open the plugin's settings in the Navidrome Admin UI.
2. Change **Trigger Scan Now** to any value it didn't have before (e.g. `now`,
   or the current time).
3. Save. The scan starts within a minute; progress appears in the Navidrome logs.

Repeating a previous value does nothing — the plugin only reacts when the value
changes. Overlapping scans are prevented with a lock, so triggering during a
running scan is a no-op.

## Installation

1. Download the latest `navidrome-bpm-plugin.ndp` release (or build it yourself).
2. Place the `.ndp` file into your Navidrome `plugins/` directory.
3. Restart or rescan plugins in Navidrome.
4. Enable the plugin via the Navidrome Admin UI.

## Build from Source

You must have [TinyGo](https://tinygo.org/) and `zip` installed.

```bash
make build
```

This will produce a `navidrome-bpm-plugin.ndp` archive in the project root, which you can drop into Navidrome.
