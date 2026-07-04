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

func TestNormalizeForMatch(t *testing.T) {
	tests := map[string]string{
		"Can't Buy Me Love":           "can t buy me love",
		"A Hard Day's Night":          "a hard day s night",
		"Help! - Remastered 2015":     "help remastered 2015",
		"From Me To You - Mono / Rem": "from me to you mono rem",
		"  extra   spaces  ":          "extra spaces",
		"Simple":                      "simple",
	}
	for input, want := range tests {
		if got := normalizeForMatch(input); got != want {
			t.Errorf("normalizeForMatch(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWordOverlap(t *testing.T) {
	// Identical sets.
	if got := wordOverlap("hello world", "hello world"); got != 1 {
		t.Errorf("identical = %v, want 1", got)
	}
	// Disjoint sets.
	if got := wordOverlap("hello world", "foo bar"); got != 0 {
		t.Errorf("disjoint = %v, want 0", got)
	}
	// Partial overlap: {a, b, c} ∩ {b, c, d} = {b, c}, union = {a, b, c, d} → 2/4 = 0.5
	if got := wordOverlap("a b c", "b c d"); got != 0.5 {
		t.Errorf("partial = %v, want 0.5", got)
	}
	// Superset: {a, b} ⊂ {a, b, c, d} → 2/4 = 0.5
	if got := wordOverlap("a b", "a b c d"); got != 0.5 {
		t.Errorf("superset = %v, want 0.5", got)
	}
	// Both empty.
	if got := wordOverlap("", ""); got != 1 {
		t.Errorf("both empty = %v, want 1", got)
	}
	// One empty.
	if got := wordOverlap("hello", ""); got != 0 {
		t.Errorf("one empty = %v, want 0", got)
	}
}
