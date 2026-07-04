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
