package scanner

import "testing"

func TestResolvePath(t *testing.T) {
	if got := resolvePath("/music", "Artist/Track.flac"); got != "/music/Artist/Track.flac" {
		t.Fatalf("relative path = %q", got)
	}
	if got := resolvePath("/music", "/srv/music/Track.flac"); got != "/srv/music/Track.flac" {
		t.Fatalf("absolute path = %q", got)
	}
}
