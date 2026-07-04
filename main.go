package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/extism/go-pdk"
	"github.com/navidrome/navidrome/plugins/pdk/go/host"
)

type SchedulerPayload struct {
	ScheduleId  string `json:"scheduleId"`
	Payload     string `json:"payload"`
	IsRecurring bool   `json:"isRecurring"`
}

type SubsonicResponse struct {
	SubsonicResponse struct {
		Status    string `json:"status"`
		Version   string `json:"version"`
		AlbumList2 struct {
			Album []Album `json:"album"`
		} `json:"albumList2"`
		Album struct {
			Song []Song `json:"song"`
		} `json:"album"`
	} `json:"subsonic-response"`
}

type Album struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type Song struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Path     string `json:"path"`
	Suffix   string `json:"suffix"`
	Duration int    `json:"duration"`
}

//go:wasmexport nd_scheduler_callback
func ndSchedulerCallback() int32 {
	var input SchedulerPayload
	if err := pdk.InputJSON(&input); err != nil {
		pdk.SetError(err)
		return 1
	}

	pdk.Log(pdk.LogInfo, "Starting periodic BPM scan...")

	// 1. Fetch all albums
	var allAlbums []Album
	offset := 0
	size := 500
	for {
		uri := fmt.Sprintf("getAlbumList2?type=newest&size=%d&offset=%d", size, offset)
		respJSON, err := host.SubsonicAPICall(uri)
		if err != nil {
			pdk.Log(pdk.LogError, fmt.Sprintf("Failed to fetch albums: %v", err))
			return 1
		}
		var sr SubsonicResponse
		if err := json.Unmarshal([]byte(respJSON), &sr); err != nil {
			pdk.Log(pdk.LogError, fmt.Sprintf("Failed to parse albums: %v", err))
			return 1
		}

		albums := sr.SubsonicResponse.AlbumList2.Album
		allAlbums = append(allAlbums, albums...)
		if len(albums) < size {
			break
		}
		offset += size
	}

	pdk.Log(pdk.LogInfo, fmt.Sprintf("Found %d albums to scan.", len(allAlbums)))

	// 2. Fetch tracks for each album and analyze
	for _, album := range allAlbums {
		uri := fmt.Sprintf("getAlbum?id=%s", album.ID)
		respJSON, err := host.SubsonicAPICall(uri)
		if err != nil {
			pdk.Log(pdk.LogError, fmt.Sprintf("Failed to fetch album %s: %v", album.ID, err))
			continue
		}
		var sr SubsonicResponse
		if err := json.Unmarshal([]byte(respJSON), &sr); err != nil {
			pdk.Log(pdk.LogError, fmt.Sprintf("Failed to parse album %s: %v", album.ID, err))
			continue
		}

		for _, song := range sr.SubsonicResponse.Album.Song {
			if strings.ToLower(song.Suffix) != "mp3" {
				continue // Only MP3 supported for now
			}

			// Check if already analyzed
			cacheKey := "bpm:" + song.ID
			_, exists, _ := host.KVStoreGet(cacheKey)
			if exists {
				continue
			}

			// Construct WASI file path. Assumes library 1 for now, but really we should iterate libraries
			// or assume Navidrome's song path maps nicely if we mount it.
			// The Library host service mounts at /libraries/<id>/
			// We can lookup libraries.
			libs, err := host.LibraryGetAllLibraries()
			if err != nil || len(libs) == 0 {
				pdk.Log(pdk.LogError, "No libraries found or permission denied")
				return 1
			}

			// Try to find the file in any library
			var bpm float64
			var analyzed bool
			for _, lib := range libs {
				localPath := fmt.Sprintf("/libraries/%d/%s", lib.ID, song.Path)
				detected, err := detectBPM(localPath)
				if err == nil {
					bpm = detected
					analyzed = true
					break
				}
			}

			if analyzed {
				pdk.Log(pdk.LogInfo, fmt.Sprintf("Analyzed %s: %.1f BPM", song.Title, bpm))
				// Save to KV Store so we don't analyze again
				bpmStr := fmt.Sprintf("%.1f", bpm)
				host.KVStoreSet(cacheKey, []byte(bpmStr))

				// TODO: Sync to playlists
				addToBPMPlaylist(song.ID, bpm)
			} else {
				pdk.Log(pdk.LogDebug, fmt.Sprintf("Failed to analyze %s (not found or error)", song.Title))
			}
		}
	}

	pdk.Log(pdk.LogInfo, "BPM scan complete.")
	return 0
}

func addToBPMPlaylist(songID string, bpm float64) {
	// Example playlist grouping by range (e.g. "BPM 120-129")
	rangeStart := int(math.Floor(bpm/10)) * 10
	playlistName := fmt.Sprintf("BPM %d-%d", rangeStart, rangeStart+9)

	// In a complete implementation, this would use getPlaylists, check if it exists,
	// createPlaylist if not, and updatePlaylist to add the song ID.
	// For brevity in this port, we log it.
	pdk.Log(pdk.LogDebug, fmt.Sprintf("Would add song %s to playlist %s", songID, playlistName))
}

//go:wasmexport nd_on_init
func ndOnInit() int32 {
	intervalHours, exists := host.ConfigGetInt("scan_interval_hours")
	if !exists {
		intervalHours = 24
	}

	cron := fmt.Sprintf("0 */%d * * *", intervalHours)
	_, err := host.SchedulerScheduleRecurring(cron, "bpm-scan", "")
	if err != nil {
		pdk.SetError(err)
		return 1
	}

	pdk.Log(pdk.LogInfo, fmt.Sprintf("BPM Plugin initialized. Scan scheduled for %s", cron))
	return 0
}

func main() {}
