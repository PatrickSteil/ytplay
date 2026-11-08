package yt

import "testing"

func TestKeyAndURLRoundTrip(t *testing.T) {
	tests := []struct{ url, key, canonical string }{
		{"https://www.youtube.com/watch?v=abc_-123", "abc_-123", "https://www.youtube.com/watch?v=abc_-123"},
		{"https://www.youtube.com/watch?v=abc_-123&list=PL1&t=30s", "abc_-123", "https://www.youtube.com/watch?v=abc_-123"},
		{"https://soundcloud.com/user.name/Track_1?si=x#t=3", "sc:user.name/Track_1", "https://soundcloud.com/user.name/Track_1"},
		{"https://www.soundcloud.com/a-b/c-d", "sc:a-b/c-d", "https://soundcloud.com/a-b/c-d"},
		{"https://m.soundcloud.com/a/b", "sc:a/b", "https://soundcloud.com/a/b"},
	}
	for _, tt := range tests {
		key := KeyOf(tt.url)
		if key != tt.key {
			t.Errorf("KeyOf(%q) = %q, want %q", tt.url, key, tt.key)
		}
		if got := URLOf(key); got != tt.canonical {
			t.Errorf("URLOf(%q) = %q, want %q", key, got, tt.canonical)
		}
	}
}

func TestKeysThatAreNotSongsAreRefused(t *testing.T) {
	for _, url := range []string{
		"", "::not a url", "https://example.com/nothing", "javascript:alert(1)",
		"https://soundcloud.com.evil.com/a/b",
		"https://evil.com/a/b",
		"https://soundcloud.com/a",        // a profile
		"https://soundcloud.com/a/sets/b", // a set
		"https://soundcloud.com/a/b/c",    // not a track
		"https://soundcloud.com/../x",     // path tricks
		"https://soundcloud.com/a/",       // no track
	} {
		if key := KeyOf(url); key != "" {
			t.Errorf("KeyOf(%q) = %q, want nothing", url, key)
		}
	}
	for _, key := range []string{"", "sc:", "sc:a", "sc:a/b/c", "sc:../x", "sc:a/..", "sc:a/", "sc:a b/c", "a b", "x/y", "v=1&list=2", "-o /tmp/x", "sc:a/likes"} {
		if got := URLOf(key); got != "" {
			t.Errorf("URLOf(%q) = %q, want nothing", key, got)
		}
	}
}

func TestSourceOfAKey(t *testing.T) {
	if SourceOf("abc") != YouTube || SourceOf("sc:a/b") != SoundCloud || SourceOf("") != YouTube {
		t.Error("wrong source")
	}
}
