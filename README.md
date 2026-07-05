# bpmd — BPM tagging daemon

A small server-side daemon that watches your music directory, detects the BPM
of new songs with [aubio](https://aubio.org/), and writes the BPM tag directly
into the files. Navidrome (or any other music server) then picks the tags up
on its next library scan — no plugin, no API integration.

This project started as a Navidrome WASM plugin, but the plugin sandbox's 30s
execution limit made accurate analysis impractical. Running next to the files
removes every constraint: full-length analysis, real tag writing, any format
ffmpeg can decode.

## How it works

```
initial full scan ──┐
                    ├──> settle delay ──> worker pool:
filesystem events ──┘                       skip if a BPM tag already exists
                                            aubio tempo  → median inter-beat BPM
                                            write tag in place (atomic rename)
```

- Files that already carry a BPM tag are skipped, so the daemon is idempotent
  and needs no state database. Use `-overwrite` to re-tag everything.
- Newly copied files are only analyzed once they stop changing (`-settle`,
  default 3s), so partial copies are never touched.
- Tags are written per format: ID3v2 `TBPM` for MP3, a `BPM` Vorbis comment
  for FLAC/OGG/Opus, and the iTunes `tmpo` atom for M4A. All writes go
  through a temp file + atomic rename.
- File mtimes change on write — deliberately, so Navidrome notices the file
  and re-reads the tag.

## Requirements

- **aubio** (required): `pacman -S aubio` (Arch), `apt install aubio-tools`
  (Debian/Ubuntu). Either the unified `aubio` CLI or the classic `aubiotrack`
  binary works; the daemon finds whichever is in `PATH`.
- **ffmpeg** (recommended): used as a decode fallback for files aubio can't
  open, and for tagging m4a/ogg/opus. Without it those formats are skipped.

## Build & install

```bash
make build            # produces ./bpmd
sudo make install     # installs to /usr/local/bin
```

## Usage

```bash
bpmd -dir /srv/music                  # watch and tag
bpmd -dir /srv/music -dry-run        # log what would be written, touch nothing
bpmd -dir /srv/music -overwrite      # re-analyze files that already have a BPM tag
bpmd -dir /srv/music -list           # print files without a BPM tag and exit
```

| Flag         | Default                    | Description                                    |
|--------------|----------------------------|------------------------------------------------|
| `-dir`       | (required)                 | Music directory to watch recursively           |
| `-ext`       | `mp3,flac,m4a,ogg,opus`    | Extensions to process                          |
| `-workers`   | half the CPU cores         | Parallel analysis workers                      |
| `-dry-run`   | off                        | Log intended writes without modifying files    |
| `-overwrite` | off                        | Overwrite existing BPM tags                    |
| `-list`      | off                        | Print files without a BPM tag, then exit       |
| `-settle`    | `3s`                       | Wait for files to stop changing before analysis |
| `-rescan`    | `0` (startup only)         | Interval between periodic full rescans         |
| `-log-level` | `info`                     | `debug`, `info`, `warn`, or `error`            |

## Running as a service

A systemd unit is provided in [`contrib/bpmd.service`](contrib/bpmd.service):

```bash
sudo cp contrib/bpmd.service /etc/systemd/system/
# edit ExecStart path/flags and User (needs write access to the music files)
sudo systemctl enable --now bpmd
```

## Notes

- Failed files (undecodable/corrupt) are logged once and not retried until
  they change or the daemon restarts.
- The daemon ignores hidden files/directories and its own `.bpmtmp` temp files.
- Detected tempos outside 40–250 BPM are rejected as implausible.
