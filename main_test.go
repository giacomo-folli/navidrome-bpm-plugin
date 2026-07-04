package main

import (
	"testing"

	"github.com/navidrome/navidrome/plugins/pdk/go/host"
)

func TestPlaylistNameForBPM(t *testing.T) {
	cases := []struct {
		tempo float64
		want  string
	}{
		{60, "BPM 60-69"},
		{124.9, "BPM 120-129"},
		{129.99, "BPM 120-129"},
		{130, "BPM 130-139"},
		{200, "BPM 200-209"},
	}
	for _, c := range cases {
		if got := playlistNameForBPM(c.tempo); got != c.want {
			t.Errorf("playlistNameForBPM(%v) = %q, want %q", c.tempo, got, c.want)
		}
	}
}

func TestLibraryFilePath(t *testing.T) {
	lib := host.Library{ID: 3, Path: "/music"}
	if got := libraryFilePath(lib, "Artist/Album/track.mp3"); got != "/libraries/3/Artist/Album/track.mp3" {
		t.Errorf("unexpected path: %q", got)
	}
	if got := libraryFilePath(lib, "/music/Artist/track.mp3"); got != "/libraries/3/Artist/track.mp3" {
		t.Errorf("expected library prefix trimmed, got %q", got)
	}
	lib.MountPoint = "/mnt/lib3"
	if got := libraryFilePath(lib, "Artist/track.mp3"); got != "/mnt/lib3/Artist/track.mp3" {
		t.Errorf("expected mount point used, got %q", got)
	}
}

func TestManualScanRequested(t *testing.T) {
	reset := func() {
		host.ConfigMock.ExpectedCalls = nil
		host.ConfigMock.Calls = nil
		host.KVStoreMock.ExpectedCalls = nil
		host.KVStoreMock.Calls = nil
	}

	t.Run("empty trigger does nothing", func(t *testing.T) {
		reset()
		host.ConfigMock.On("Get", "trigger_scan").Return("", false).Once()
		if manualScanRequested() {
			t.Error("expected no scan for unset trigger")
		}
	})

	t.Run("unchanged trigger does nothing", func(t *testing.T) {
		reset()
		host.ConfigMock.On("Get", "trigger_scan").Return("now", true).Once()
		host.KVStoreMock.On("Get", kvLastTrigger).Return([]byte("now"), true, nil).Once()
		if manualScanRequested() {
			t.Error("expected no scan for unchanged trigger")
		}
	})

	t.Run("new trigger requests scan and is recorded", func(t *testing.T) {
		reset()
		host.ConfigMock.On("Get", "trigger_scan").Return("now-2", true).Once()
		host.KVStoreMock.On("Get", kvLastTrigger).Return([]byte("now"), true, nil).Once()
		host.KVStoreMock.On("Set", kvLastTrigger, []byte("now-2")).Return(nil).Once()
		if !manualScanRequested() {
			t.Error("expected scan for changed trigger")
		}
		host.KVStoreMock.AssertExpectations(t)
	})
}

func TestPlaylistSyncAddSong(t *testing.T) {
	host.SubsonicAPIMock.ExpectedCalls = nil
	host.SubsonicAPIMock.Calls = nil

	host.SubsonicAPIMock.On("Call", "getPlaylists").Return(
		`{"subsonic-response":{"status":"ok","playlists":{"playlist":[{"id":"pl1","name":"BPM 120-129"}]}}}`, nil).Once()
	host.SubsonicAPIMock.On("Call", "updatePlaylist?playlistId=pl1&songIdToAdd=song1").Return(
		`{"subsonic-response":{"status":"ok"}}`, nil).Once()
	host.SubsonicAPIMock.On("Call", "createPlaylist?name=BPM+90-99&songId=song2").Return(
		`{"subsonic-response":{"status":"ok","playlist":{"id":"pl2","name":"BPM 90-99"}}}`, nil).Once()
	host.SubsonicAPIMock.On("Call", "updatePlaylist?playlistId=pl2&songIdToAdd=song3").Return(
		`{"subsonic-response":{"status":"ok"}}`, nil).Once()

	sync := &playlistSync{}
	// Existing playlist -> update.
	if err := sync.addSong("song1", 124.3); err != nil {
		t.Fatalf("addSong(song1): %v", err)
	}
	// Missing playlist -> create, then reuse the returned ID.
	if err := sync.addSong("song2", 95.0); err != nil {
		t.Fatalf("addSong(song2): %v", err)
	}
	if err := sync.addSong("song3", 92.1); err != nil {
		t.Fatalf("addSong(song3): %v", err)
	}

	host.SubsonicAPIMock.AssertExpectations(t)
}
