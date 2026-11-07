package ui

import (
	"sync"

	"github.com/PatrickSteil/ytplay/internal/autodj"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

// analysisStore holds what the analyses found out about songs, by video ID, in
// a form the player can look up from its own goroutines: the model itself is
// only ever touched from Update.
type analysisStore struct {
	mu    sync.Mutex
	songs map[string]autodj.Song
}

func newAnalysisStore() *analysisStore { return &analysisStore{songs: map[string]autodj.Song{}} }

func (s *analysisStore) put(id string, song autodj.Song) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.songs[id] = song
}

// lookup finds a song by its watch URL.
func (s *analysisStore) lookup(url string) (autodj.Song, bool) {
	id := yt.VideoID(url)
	if id == "" {
		return autodj.Song{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	song, ok := s.songs[id]
	return song, ok
}
