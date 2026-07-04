package scanner

import "testing"

func TestResolvePath(t *testing.T) {
	if got := resolvePath("/host/music", "/music", "Artist/Track.flac"); got != "/host/music/Artist/Track.flac" {
		t.Fatalf("relative path = %q", got)
	}
	if got := resolvePath("/host/music", "/music", "/music/Artist/Track.flac"); got != "/host/music/Artist/Track.flac" {
		t.Fatalf("mapped absolute path = %q", got)
	}
	if got := resolvePath("/host/music", "/music", "/srv/music/Track.flac"); got != "/srv/music/Track.flac" {
		t.Fatalf("absolute path = %q", got)
	}
}

func TestStripTrackPrefixPath(t *testing.T) {
	got := stripTrackPrefixPath("/music/Colla Zio/Zafferano/01-04 - Sola La Notte.mp3")
	want := "/music/Colla Zio/Zafferano/Sola La Notte.mp3"
	if got != want {
		t.Fatalf("stripTrackPrefixPath() = %q, want %q", got, want)
	}

	got = stripTrackPrefixPath("/music/Artist/04 - Song.flac")
	want = "/music/Artist/Song.flac"
	if got != want {
		t.Fatalf("stripTrackPrefixPath() = %q, want %q", got, want)
	}

	got = stripTrackPrefixPath("/music/Artist/Song.flac")
	want = "/music/Artist/Song.flac"
	if got != want {
		t.Fatalf("stripTrackPrefixPath() = %q, want %q", got, want)
	}
}
