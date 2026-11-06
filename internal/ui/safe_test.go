package ui

import (
	"strings"
	"testing"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

func TestSafeRemovesEmojiAndJoiners(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Best 🔥 Songs", "Best Songs"},
		{"I ❤️ this", "I this"},              // U+2764 U+FE0F
		{"I ♥ this", "I this"},               // text-style heart
		{"family 👨‍👩‍👧 time", "family time"}, // ZWJ sequence
		{"🇩🇪 Germany", "Germany"},            // regional indicators (flag)
		{"👍🏽 nice", "nice"},                  // skin tone modifier
		{"1️⃣ first", "1 first"},             // keycap sequence
		{"wow✨", "wow"},
		{"zero​width", "zerowidth"}, // zero width space
		{"a‮b", "ab"},               // bidi override
		{"⭐ star ⏸ pause ▶ play ☀", "star pause play"},
		{"  spaced   out  ", "spaced out"},
		{"", ""},
		{"🔥🔥🔥", ""},
		// Must survive: letters, accents, CJK, punctuation, symbols with a stable width.
		{"Café — naïve 日本語 © 2013 → ok ™", "Café — naïve 日本語 © 2013 → ok ™"},
		{"100% (live) [HD] #1 & more…", "100% (live) [HD] #1 & more…"},
	}
	for _, tt := range tests {
		if got := safe(tt.in); got != tt.want {
			t.Errorf("safe(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSafeLeavesNoEmojiBehind(t *testing.T) {
	// Every emoji-ish code point in the main blocks must be dropped.
	var sb strings.Builder
	for r := rune(0x1F000); r <= 0x1FAFF; r += 7 {
		sb.WriteRune(r)
	}
	for r := rune(0x2600); r <= 0x27BF; r += 3 {
		sb.WriteRune(r)
	}
	if got := safe("x" + sb.String() + "y"); got != "xy" {
		t.Errorf("emoji survived: %q", got)
	}
	for _, r := range safe("é日a1") {
		if !unicode.IsPrint(r) {
			t.Errorf("unprintable rune %U survived", r)
		}
	}
}

// emojiModel plays a track whose title, channel, description and comments are
// full of emoji.
func emojiModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := queueModel(t, 3)
	m.tracks["e0"] = yt.Result{ID: "e0", Title: "Song 🔥 One ❤️", Channel: "Chan 🎵 A", Duration: 100}
	m.tracks["e1"] = yt.Result{ID: "e1", Title: "👨‍👩‍👧 Family 🇩🇪 Two", Channel: "B", Duration: 100}
	m = at(m, w, h)
	m, _ = update(m, infoMsg{id: "e0", info: yt.Info{
		Description: "Great 🎶 track ✨\nLine two ❤️❤️",
		Comments: []yt.Comment{
			{Text: "🔥🔥🔥 love this 😍 song 👍🏽", Likes: 4800},
			{Text: "🇩🇪 Hallo from Germany 👨‍👩‍👧", Likes: 12},
		},
		Views: 5000, Likes: 100, Year: 2020,
	}})
	m, _ = update(m, analysisMsg{id: "e0", res: analyze.Result{BPM: 120, Key: "C major", Camelot: "8B"}})
	m.results = []yt.Result{{ID: "r1", Title: "Result 🔥 Title ❤️", Channel: "Chan ✨", Duration: 60}}
	m.query = "emoji 🔥 query"
	return m
}

// fixtureEmoji are the emoji used in emojiModel.
var fixtureEmoji = []string{"🔥", "❤", "🎵", "👨", "👩", "👧", "🇩", "🇪", "✨", "🎶", "😍", "👍", "🏽", "♥", "\u200d", "\ufe0f"}

func containsEmoji(s string) bool {
	for _, e := range fixtureEmoji {
		if strings.Contains(s, e) {
			return true
		}
	}
	return false
}

func TestExternalTextNeverReachesTheScreenWithEmoji(t *testing.T) {
	for _, size := range [][2]int{{140, 44}, {108, 30}, {90, 40}} {
		m := emojiModel(t, size[0], size[1])
		m.setStickyStatus("Playing: Song 🔥 One ❤️")
		out := ansi.Strip(m.View())
		if containsEmoji(out) {
			t.Errorf("%v: emoji on screen:\n%s", size, out)
		}
		for _, want := range []string{"Song One", "love this song", "Hallo from Germany", "Great track", "Result Title"} {
			if size[0] >= 108 || want == "Song One" {
				if !strings.Contains(out, want) {
					t.Errorf("%v: missing %q in:\n%s", size, want, out)
				}
			}
		}
	}
}

func TestEmojiCannotBreakTheFrames(t *testing.T) {
	for _, size := range [][2]int{{108, 30}, {140, 44}, {200, 50}} {
		m := emojiModel(t, size[0], size[1])
		m.mode = modeResults
		for i, l := range viewLines(m) {
			if i < len(viewLines(m))-1 && lipgloss.Width(l) != size[0] {
				t.Errorf("%v: line %d is %d wide, want %d: %q", size, i, lipgloss.Width(l), size[0], ansi.Strip(l))
			}
		}
	}
}

func TestOwnSymbolsAreNotTouched(t *testing.T) {
	m := queueModel(t, 3)
	m = at(m, 140, 30)
	m, _ = update(m, positionMsg{elapsed: 5, total: 100})
	out := ansi.Strip(m.View())
	for _, want := range []string{"▶", "⏎", "⇥", "│", "┌"} {
		if !strings.Contains(out, want) {
			t.Errorf("the UI's own symbol %q was removed", want)
		}
	}
}
