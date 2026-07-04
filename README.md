# Navidrome BPM Plugin

A native WebAssembly plugin for Navidrome that analyzes local music files, detects their BPM using a pure-Go implementation, and categorizes them into playlists.

## Features

- **Native Navidrome Plugin**: Runs entirely within Navidrome's secure WebAssembly sandbox (Extism). No external daemons, docker containers, or API polling required.
- **Pure-Go BPM Detection**: Uses `go-mp3` and `benjojo/bpm` to decode and analyze MP3s in-memory without relying on host system binaries like `aubio`.
- **Zero Configuration Setup**: Configure the scan interval and target playlist names directly from the Navidrome Web UI.
- **Efficient Caching**: Remembers analyzed files using Navidrome's native `KVStore` host service.

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
