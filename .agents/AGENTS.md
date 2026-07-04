# Navidrome BPM Plugin Development Guidelines

This document provides instructions and architectural context for agents working on the `navidrome-bpm-plugin-codex` project.

## 1. Native Plugin Architecture

This project is a **Native Navidrome Plugin**, not a standalone daemon or external agent. It runs inside Navidrome's Extism WebAssembly (Wasm) sandbox.

* **Language/Compiler**: Go, compiled to WebAssembly via **TinyGo**.
* **Target**: `wasip1` with `-buildmode=c-shared`.
* **Packaging**: The `.wasm` binary and `manifest.json` are packaged into an `.ndp` (zip) archive.
* **Sandbox Constraints**:
  * **No CGO**: You cannot use C bindings.
  * **No Host Executables**: You cannot use `exec.Command` (e.g., `aubio` CLI is forbidden).
  * **Read-Only Filesystem**: The plugin can only read audio files from the Navidrome library mounts. It cannot write ID3 tags directly.

Additional ref: 
* https://www.navidrome.org/docs/usage/features/plugins/
* https://github.com/navidrome/navidrome/blob/master/plugins/README.md


## 2. Overall Structure

The project has been minimized to fit the native plugin paradigm:

* `manifest.json`: Defines plugin metadata, UI configuration schema (e.g. `scan_interval_hours`), and required permissions.
* `main.go`: The Wasm entrypoint. Contains capability exports (`nd_on_init`, `nd_scheduler_callback`) and orchestrates logic.
* `bpm.go`: The core business logic. Since we are restricted to pure Go, it handles MP3 decoding (via `hajimehoshi/go-mp3`) and in-memory BPM calculation (via `benjojo/bpm`).
* `Makefile`: Contains the `build` target to compile with TinyGo and zip the `.ndp` file.

## 3. Host Services & Integration

Do not use custom HTTP clients, standalone databases, or external file crawlers. Always utilize Navidrome's Native Host Services:

* **Configuration**: Read settings via `host.ConfigGetInt` or `host.ConfigGetString` instead of environment variables or `.env` files.
* **Scheduling**: The plugin is event-driven. We use the `SchedulerCallback` capability (exported as `nd_scheduler_callback`) to wake up periodically.
* **State/Caching**: Use the `KVStore` host service (`host.KVStoreGet`, `host.KVStoreSet`) to track which tracks have been analyzed, rather than a local SQLite database.
* **Library Access**: Use the `Library` host service. The library permission with `filesystem: true` mounts the Navidrome music directories at `/libraries/<id>/`. Use this to read audio files for analysis.
* **Navidrome Mutability**: Because the filesystem is read-only, write BPM metadata to Navidrome by creating/updating Playlists using the `SubsonicAPI` host service (`host.SubsonicAPICall`).

## 4. Development Best Practices

* **PDK Imports**: Use `github.com/extism/go-pdk` for core Extism functions and `github.com/navidrome/navidrome/plugins/pdk/go/host` for Navidrome-specific host services.
* **Logging**: Use the Extism logging facilities: `pdk.Log(pdk.LogInfo, "message")`. Do not use standard `log` or `slog` packages printing to stdout, as Wasm logging is handled by the host.
* **Pure Go**: If you need new functionality (like FLAC decoding), ensure you find a pure-Go library that is compatible with TinyGo.
