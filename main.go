package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/navidrome/navidrome/plugins/pdk/go/host"
	"github.com/navidrome/navidrome/plugins/pdk/go/lifecycle"
	"github.com/navidrome/navidrome/plugins/pdk/go/pdk"
	"github.com/navidrome/navidrome/plugins/pdk/go/scheduler"
)

const (
	scanScheduleID    = "bpm-scan"
	triggerScheduleID = "bpm-trigger-check"
	processScheduleID = "bpm-process"

	kvLastTrigger = "trigger:last"
	kvScanLock    = "scan:lock"
	kvScanQueue   = "scan:queue"
	kvScanStats   = "scan:stats"

	// Navidrome kills any plugin call after 30s, so each batch stops
	// analyzing after this budget and chains a follow-up one-time task.
	batchTimeBudget = 15 * time.Second
	// scanLockTTL only needs to outlive the gap between chained batches; if a
	// batch is killed, the lock expires and the next scan resumes the queue.
	scanLockTTL = 180
	// pendingTTL bounds how long a song can be marked in-flight before a
	// retry would treat it as poisonous again.
	pendingTTL = 3600
)

type bpmPlugin struct{}

func init() {
	lifecycle.Register(&bpmPlugin{})
	scheduler.Register(&bpmPlugin{})
}

func (p *bpmPlugin) OnInit() error {
	spec := ""
	if minutes, ok := host.ConfigGetInt("scan_interval_minutes"); ok && minutes > 0 {
		spec = fmt.Sprintf("@every %dm", minutes)
	} else {
		hours, ok := host.ConfigGetInt("scan_interval_hours")
		if !ok || hours <= 0 {
			hours = 24
		}
		spec = fmt.Sprintf("@every %dh", hours)
	}
	if _, err := host.SchedulerScheduleRecurring(spec, scanScheduleID, scanScheduleID); err != nil {
		return fmt.Errorf("failed to schedule BPM scan: %w", err)
	}
	// Poll the trigger_scan config value so users can request a scan from the
	// plugin's config UI (there is no way to invoke a plugin directly).
	if _, err := host.SchedulerScheduleRecurring("@every 1m", triggerScheduleID, triggerScheduleID); err != nil {
		return fmt.Errorf("failed to schedule trigger check: %w", err)
	}

	// The plugin was just (re)loaded, so no scan can be running: clear any
	// lock left behind by a killed batch (its defer never ran).
	host.KVStoreDelete(kvScanLock)

	pdk.Log(pdk.LogInfo, fmt.Sprintf("BPM plugin initialized, scan scheduled %s", spec))
	return nil
}

func (p *bpmPlugin) OnCallback(req scheduler.SchedulerCallbackRequest) error {
	switch req.ScheduleID {
	case triggerScheduleID:
		if !manualScanRequested() {
			return nil
		}
		pdk.Log(pdk.LogInfo, "Manual scan requested via trigger_scan config")
		return startScan()
	case scanScheduleID:
		return startScan()
	case processScheduleID:
		return processBatch()
	default:
		pdk.Log(pdk.LogWarn, fmt.Sprintf("Ignoring unknown schedule %q", req.ScheduleID))
		return nil
	}
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

type scanStats struct {
	Analyzed int `json:"analyzed"`
	Failed   int `json:"failed"`
}

// startScan builds the album work queue and processes the first batch. If a
// queue is left over from an interrupted scan, it is resumed instead.
func startScan() error {
	if _, running, _ := host.KVStoreGet(kvScanLock); running {
		pdk.Log(pdk.LogInfo, "A BPM scan is already running, skipping.")
		return nil
	}

	if _, pending, _ := host.KVStoreGet(kvScanQueue); pending {
		pdk.Log(pdk.LogInfo, "Resuming interrupted BPM scan.")
		return processBatch()
	}

	pdk.Log(pdk.LogInfo, "Starting BPM scan...")

	if libs, err := host.LibraryGetAllLibraries(); err == nil {
		logLibraryMounts(libs)
	}

	client, err := newSubsonicClient()
	if err != nil {
		return err
	}
	albums, err := client.fetchAllAlbums()
	if err != nil {
		return err
	}
	pdk.Log(pdk.LogInfo, fmt.Sprintf("Found %d albums to scan.", len(albums)))

	ids := make([]string, len(albums))
	for i, a := range albums {
		ids[i] = a.ID
	}
	if err := saveQueue(ids); err != nil {
		return err
	}
	if err := saveStats(scanStats{}); err != nil {
		return err
	}
	return processBatch()
}

// processBatch analyzes songs until the time budget is spent, then either
// schedules the next batch or finishes the scan.
func processBatch() error {
	if err := host.KVStoreSetWithTTL(kvScanLock, []byte("1"), scanLockTTL); err != nil {
		return fmt.Errorf("failed to refresh scan lock: %w", err)
	}

	queue, err := loadQueue()
	if err != nil {
		return err
	}
	stats, err := loadStats()
	if err != nil {
		return err
	}

	libs, err := host.LibraryGetAllLibraries()
	if err != nil {
		return fmt.Errorf("failed to list libraries: %w", err)
	}
	if len(libs) == 0 {
		pdk.Log(pdk.LogWarn, "No libraries available, aborting BPM scan")
		return finishScan(stats)
	}
	client, err := newSubsonicClient()
	if err != nil {
		return err
	}
	sync := &playlistSync{client: client}

	deadline := time.Now().Add(batchTimeBudget)
	for len(queue) > 0 && time.Now().Before(deadline) {
		albumID := queue[0]
		songs, err := client.fetchAlbumSongs(albumID)
		if err != nil {
			pdk.Log(pdk.LogError, fmt.Sprintf("Failed to fetch album %s: %v", albumID, err))
			queue = queue[1:]
			continue
		}

		done := true
		for _, s := range songs {
			if time.Now().After(deadline) {
				done = false // revisit this album next batch; analyzed songs are cached
				break
			}
			processSong(libs, sync, s, &stats)
		}
		if done {
			queue = queue[1:]
		}
	}

	if err := saveStats(stats); err != nil {
		return err
	}
	if len(queue) == 0 {
		return finishScan(stats)
	}
	if err := saveQueue(queue); err != nil {
		return err
	}
	if _, err := host.SchedulerScheduleOneTime(1, processScheduleID, processScheduleID); err != nil {
		return fmt.Errorf("failed to schedule next batch: %w", err)
	}
	pdk.Log(pdk.LogInfo, fmt.Sprintf("BPM batch done (%d analyzed, %d failed so far), %d albums remaining.",
		stats.Analyzed, stats.Failed, len(queue)))
	return nil
}

func processSong(libs []host.Library, sync *playlistSync, s song, stats *scanStats) {
	if !strings.EqualFold(s.Suffix, "mp3") {
		return // Only MP3 supported for now
	}
	cacheKey := "bpm:" + s.ID
	if _, exists, _ := host.KVStoreGet(cacheKey); exists {
		return
	}

	// A leftover pending marker means a previous attempt was killed by the
	// host mid-analysis; don't retry it or it will poison every batch.
	pendingKey := "pending:" + s.ID
	if _, crashed, _ := host.KVStoreGet(pendingKey); crashed {
		pdk.Log(pdk.LogWarn, fmt.Sprintf("Skipping %q: previous analysis attempt did not finish", s.Path))
		host.KVStoreSet(cacheKey, []byte("failed"))
		host.KVStoreDelete(pendingKey)
		stats.Failed++
		return
	}
	host.KVStoreSetWithTTL(pendingKey, []byte("1"), pendingTTL)

	tempo, err := analyzeSong(libs, s)
	host.KVStoreDelete(pendingKey)
	if err != nil {
		stats.Failed++
		if stats.Failed <= maxFailureWarnings {
			pdk.Log(pdk.LogWarn, fmt.Sprintf("Failed to analyze %q: %v", s.Path, err))
		}
		host.KVStoreSet(cacheKey, []byte("failed"))
		return
	}
	stats.Analyzed++
	pdk.Log(pdk.LogInfo, fmt.Sprintf("Analyzed %s: %.1f BPM", s.Title, tempo))

	if err := host.KVStoreSet(cacheKey, []byte(fmt.Sprintf("%.1f", tempo))); err != nil {
		pdk.Log(pdk.LogError, fmt.Sprintf("Failed to store BPM for %s: %v", s.ID, err))
	}
	if err := sync.addSong(s.ID, tempo); err != nil {
		pdk.Log(pdk.LogWarn, fmt.Sprintf("Failed to add %s to playlist: %v", s.Title, err))
	}
}

func finishScan(stats scanStats) error {
	host.KVStoreDelete(kvScanQueue)
	host.KVStoreDelete(kvScanStats)
	host.KVStoreDelete(kvScanLock)
	pdk.Log(pdk.LogInfo, fmt.Sprintf("BPM scan complete: %d analyzed, %d failed.", stats.Analyzed, stats.Failed))
	if stats.Analyzed == 0 && stats.Failed > 0 {
		pdk.Log(pdk.LogWarn, "All analyses failed. If the errors say the file does not exist, the Subsonic API "+
			"is reporting fake paths: enable 'Report Real Path' for this plugin's player "+
			"(Settings > Players > navidrome-bpm-plugin), or set ND_SUBSONIC_DEFAULTREPORTREALPATH=true "+
			"and delete the plugin's player so it re-registers.")
	}
	return nil
}

func saveQueue(ids []string) error {
	data, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if err := host.KVStoreSet(kvScanQueue, data); err != nil {
		return fmt.Errorf("failed to save scan queue: %w", err)
	}
	return nil
}

func loadQueue() ([]string, error) {
	data, exists, err := host.KVStoreGet(kvScanQueue)
	if err != nil {
		return nil, fmt.Errorf("failed to load scan queue: %w", err)
	}
	if !exists {
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return nil, fmt.Errorf("corrupt scan queue: %w", err)
	}
	return ids, nil
}

func saveStats(stats scanStats) error {
	data, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	return host.KVStoreSet(kvScanStats, data)
}

func loadStats() (scanStats, error) {
	var stats scanStats
	data, exists, err := host.KVStoreGet(kvScanStats)
	if err != nil || !exists {
		return stats, err
	}
	if err := json.Unmarshal(data, &stats); err != nil {
		return scanStats{}, fmt.Errorf("corrupt scan stats: %w", err)
	}
	return stats, nil
}

// maxFailureWarnings caps per-song warn logs per scan; analyzeSong errors
// beyond it are still counted but not logged individually.
const maxFailureWarnings = 5

// analyzeSong tries the song's path under each library mount until one decodes.
func analyzeSong(libs []host.Library, s song) (float64, error) {
	var errs []string
	for _, lib := range libs {
		filePath := libraryFilePath(lib, s.Path)
		tempo, err := detectBPM(filePath)
		if err == nil {
			return tempo, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", filePath, err))
	}
	return 0, fmt.Errorf("%s", strings.Join(errs, "; "))
}

// logLibraryMounts records each library's WASI mount and whether it is
// readable, to make path mapping problems obvious in the logs.
func logLibraryMounts(libs []host.Library) {
	for _, lib := range libs {
		mount := libraryMount(lib)
		entries, err := os.ReadDir(mount)
		if err != nil {
			pdk.Log(pdk.LogWarn, fmt.Sprintf("Library %d (%s): mount %s not readable: %v", lib.ID, lib.Name, mount, err))
		} else {
			pdk.Log(pdk.LogInfo, fmt.Sprintf("Library %d (%s): mounted at %s (%d entries)", lib.ID, lib.Name, mount, len(entries)))
		}
	}
}

func libraryMount(lib host.Library) string {
	if lib.MountPoint != "" {
		return lib.MountPoint
	}
	return fmt.Sprintf("/libraries/%d", lib.ID)
}

// libraryFilePath maps a Subsonic song path to the plugin's read-only WASI
// mount for that library. Paths may be relative to the library root or
// absolute (when the player has "Report Real Path" enabled).
func libraryFilePath(lib host.Library, songPath string) string {
	if lib.Path != "" && strings.HasPrefix(songPath, lib.Path) {
		songPath = strings.TrimPrefix(songPath, lib.Path)
	}
	return path.Join(libraryMount(lib), songPath)
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

// subsonicClient appends the required u=<username> parameter to every call.
// The username must be one the admin authorized for this plugin in the UI.
type subsonicClient struct {
	user string
}

func newSubsonicClient() (*subsonicClient, error) {
	users, err := host.UsersGetAdmins()
	if err != nil || len(users) == 0 {
		users, err = host.UsersGetUsers()
		if err != nil {
			return nil, fmt.Errorf("failed to list authorized users: %w", err)
		}
	}
	if len(users) == 0 {
		return nil, fmt.Errorf("no users authorized for this plugin; grant access in the plugin settings")
	}
	return &subsonicClient{user: users[0].UserName}, nil
}

func (c *subsonicClient) call(uri string) (*subsonicResponse, error) {
	sep := "?"
	if strings.Contains(uri, "?") {
		sep = "&"
	}
	respJSON, err := host.SubsonicAPICall(uri + sep + "u=" + url.QueryEscape(c.user))
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

func (c *subsonicClient) fetchAllAlbums() ([]album, error) {
	var all []album
	const size = 500
	for offset := 0; ; offset += size {
		uri := fmt.Sprintf("getAlbumList2?type=alphabeticalByName&size=%d&offset=%d", size, offset)
		sr, err := c.call(uri)
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

func (c *subsonicClient) fetchAlbumSongs(albumID string) ([]song, error) {
	sr, err := c.call("getAlbum?id=" + url.QueryEscape(albumID))
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
	client *subsonicClient
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
		_, err := p.client.call(uri)
		return err
	}

	uri := fmt.Sprintf("createPlaylist?name=%s&songId=%s",
		url.QueryEscape(name), url.QueryEscape(songID))
	sr, err := p.client.call(uri)
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
	sr, err := p.client.call("getPlaylists")
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
