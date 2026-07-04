package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/navidrome/navidrome/plugins/pdk/go/host"
	"github.com/navidrome/navidrome/plugins/pdk/go/lifecycle"
	"github.com/navidrome/navidrome/plugins/pdk/go/pdk"
	"github.com/navidrome/navidrome/plugins/pdk/go/scheduler"
)

const (
	scanScheduleID    = "bpm-scan"
	triggerScheduleID = "bpm-trigger-check"

	kvLastTrigger = "trigger:last"
	kvScanLock    = "scan:lock"
	// scanLockTTL bounds how long a crashed scan can block new ones.
	scanLockTTL = 2 * 60 * 60
)

type bpmPlugin struct{}

func init() {
	lifecycle.Register(&bpmPlugin{})
	scheduler.Register(&bpmPlugin{})
}

func (p *bpmPlugin) OnInit() error {
	interval, ok := host.ConfigGetInt("scan_interval_hours")
	if !ok || interval <= 0 {
		interval = 24
	}

	spec := fmt.Sprintf("@every %dh", interval)
	if _, err := host.SchedulerScheduleRecurring(spec, scanScheduleID, scanScheduleID); err != nil {
		return fmt.Errorf("failed to schedule BPM scan: %w", err)
	}
	// Poll the trigger_scan config value so users can request a scan from the
	// plugin's config UI (there is no way to invoke a plugin directly).
	if _, err := host.SchedulerScheduleRecurring("@every 1m", triggerScheduleID, triggerScheduleID); err != nil {
		return fmt.Errorf("failed to schedule trigger check: %w", err)
	}

	pdk.Log(pdk.LogInfo, fmt.Sprintf("BPM plugin initialized, scan scheduled %s", spec))
	return nil
}

func (p *bpmPlugin) OnCallback(req scheduler.SchedulerCallbackRequest) error {
	if req.ScheduleID == triggerScheduleID {
		if !manualScanRequested() {
			return nil
		}
		pdk.Log(pdk.LogInfo, "Manual scan requested via trigger_scan config")
	}
	return runScan()
}

// manualScanRequested reports whether the trigger_scan config value changed
// since the last manual scan, and records the new value.
func manualScanRequested() bool {
	val, ok := host.ConfigGet("trigger_scan")
	if !ok || val == "" {
		return false
	}
	last, _, _ := host.KVStoreGet(kvLastTrigger)
	if string(last) == val {
		return false
	}
	if err := host.KVStoreSet(kvLastTrigger, []byte(val)); err != nil {
		pdk.Log(pdk.LogError, fmt.Sprintf("Failed to record trigger value: %v", err))
		return false
	}
	return true
}

func runScan() error {
	// A manual trigger can fire while a periodic scan is running (and vice
	// versa); a TTL-bounded KV lock keeps them from overlapping.
	if _, running, _ := host.KVStoreGet(kvScanLock); running {
		pdk.Log(pdk.LogInfo, "A BPM scan is already running, skipping.")
		return nil
	}
	if err := host.KVStoreSetWithTTL(kvScanLock, []byte("1"), scanLockTTL); err != nil {
		return fmt.Errorf("failed to acquire scan lock: %w", err)
	}
	defer host.KVStoreDelete(kvScanLock)

	pdk.Log(pdk.LogInfo, "Starting BPM scan...")

	libs, err := host.LibraryGetAllLibraries()
	if err != nil {
		return fmt.Errorf("failed to list libraries: %w", err)
	}
	if len(libs) == 0 {
		pdk.Log(pdk.LogWarn, "No libraries available, skipping BPM scan")
		return nil
	}

	albums, err := fetchAllAlbums()
	if err != nil {
		return err
	}
	pdk.Log(pdk.LogInfo, fmt.Sprintf("Found %d albums to scan.", len(albums)))

	sync := &playlistSync{}
	analyzed, failed := 0, 0
	for _, alb := range albums {
		songs, err := fetchAlbumSongs(alb.ID)
		if err != nil {
			pdk.Log(pdk.LogError, fmt.Sprintf("Failed to fetch album %s: %v", alb.ID, err))
			continue
		}

		for _, s := range songs {
			if !strings.EqualFold(s.Suffix, "mp3") {
				continue // Only MP3 supported for now
			}

			cacheKey := "bpm:" + s.ID
			if _, exists, _ := host.KVStoreGet(cacheKey); exists {
				continue
			}

			tempo, ok := analyzeSong(libs, s)
			if !ok {
				failed++
				continue
			}
			analyzed++
			pdk.Log(pdk.LogInfo, fmt.Sprintf("Analyzed %s: %.1f BPM", s.Title, tempo))

			if err := host.KVStoreSet(cacheKey, []byte(fmt.Sprintf("%.1f", tempo))); err != nil {
				pdk.Log(pdk.LogError, fmt.Sprintf("Failed to store BPM for %s: %v", s.ID, err))
			}
			if err := sync.addSong(s.ID, tempo); err != nil {
				pdk.Log(pdk.LogWarn, fmt.Sprintf("Failed to add %s to playlist: %v", s.Title, err))
			}
		}
	}

	pdk.Log(pdk.LogInfo, fmt.Sprintf("BPM scan complete: %d analyzed, %d failed.", analyzed, failed))
	return nil
}

// analyzeSong tries the song's path under each library mount until one decodes.
func analyzeSong(libs []host.Library, s song) (float64, bool) {
	for _, lib := range libs {
		tempo, err := detectBPM(libraryFilePath(lib, s.Path))
		if err == nil {
			return tempo, true
		}
		pdk.Log(pdk.LogDebug, fmt.Sprintf("Analysis of %s in library %d failed: %v", s.Path, lib.ID, err))
	}
	return 0, false
}

// libraryFilePath maps a Subsonic song path (relative to the library root) to
// the plugin's read-only WASI mount for that library.
func libraryFilePath(lib host.Library, songPath string) string {
	base := lib.MountPoint
	if base == "" {
		base = fmt.Sprintf("/libraries/%d", lib.ID)
	}
	if lib.Path != "" && strings.HasPrefix(songPath, lib.Path) {
		songPath = strings.TrimPrefix(songPath, lib.Path)
	}
	return path.Join(base, songPath)
}

// --- Subsonic API ---

type subsonicResponse struct {
	SubsonicResponse struct {
		Status string `json:"status"`
		Error  struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		AlbumList2 struct {
			Album []album `json:"album"`
		} `json:"albumList2"`
		Album struct {
			Song []song `json:"song"`
		} `json:"album"`
		Playlists struct {
			Playlist []playlist `json:"playlist"`
		} `json:"playlists"`
		Playlist playlist `json:"playlist"`
	} `json:"subsonic-response"`
}

type album struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type song struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Path   string `json:"path"`
	Suffix string `json:"suffix"`
}

type playlist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func subsonicCall(uri string) (*subsonicResponse, error) {
	respJSON, err := host.SubsonicAPICall(uri)
	if err != nil {
		return nil, err
	}
	var sr subsonicResponse
	if err := json.Unmarshal([]byte(respJSON), &sr); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if sr.SubsonicResponse.Status == "failed" {
		return nil, fmt.Errorf("subsonic error %d: %s",
			sr.SubsonicResponse.Error.Code, sr.SubsonicResponse.Error.Message)
	}
	return &sr, nil
}

func fetchAllAlbums() ([]album, error) {
	var all []album
	const size = 500
	for offset := 0; ; offset += size {
		uri := fmt.Sprintf("getAlbumList2?type=alphabeticalByName&size=%d&offset=%d", size, offset)
		sr, err := subsonicCall(uri)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch albums: %w", err)
		}
		batch := sr.SubsonicResponse.AlbumList2.Album
		all = append(all, batch...)
		if len(batch) < size {
			return all, nil
		}
	}
}

func fetchAlbumSongs(albumID string) ([]song, error) {
	sr, err := subsonicCall("getAlbum?id=" + url.QueryEscape(albumID))
	if err != nil {
		return nil, err
	}
	return sr.SubsonicResponse.Album.Song, nil
}

// --- Playlist sync ---

// playlistSync groups analyzed songs into "BPM 120-129" style playlists,
// which is how the plugin exposes BPM values (the library filesystem is
// read-only, so tags cannot be written).
type playlistSync struct {
	byName map[string]string // playlist name -> ID, loaded lazily per scan
}

func playlistNameForBPM(tempo float64) string {
	rangeStart := int(tempo/10) * 10
	return fmt.Sprintf("BPM %d-%d", rangeStart, rangeStart+9)
}

func (p *playlistSync) addSong(songID string, tempo float64) error {
	if p.byName == nil {
		if err := p.load(); err != nil {
			return err
		}
	}

	name := playlistNameForBPM(tempo)
	if id, ok := p.byName[name]; ok {
		uri := fmt.Sprintf("updatePlaylist?playlistId=%s&songIdToAdd=%s",
			url.QueryEscape(id), url.QueryEscape(songID))
		_, err := subsonicCall(uri)
		return err
	}

	uri := fmt.Sprintf("createPlaylist?name=%s&songId=%s",
		url.QueryEscape(name), url.QueryEscape(songID))
	sr, err := subsonicCall(uri)
	if err != nil {
		return err
	}
	if id := sr.SubsonicResponse.Playlist.ID; id != "" {
		p.byName[name] = id
	} else {
		// Server didn't return the new playlist; re-fetch so the next song in
		// this range updates it instead of creating a duplicate.
		return p.load()
	}
	return nil
}

func (p *playlistSync) load() error {
	sr, err := subsonicCall("getPlaylists")
	if err != nil {
		return fmt.Errorf("failed to fetch playlists: %w", err)
	}
	p.byName = make(map[string]string)
	for _, pl := range sr.SubsonicResponse.Playlists.Playlist {
		p.byName[pl.Name] = pl.ID
	}
	return nil
}

func main() {}
