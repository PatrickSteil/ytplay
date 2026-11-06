// Package player turns user actions into mpv commands. mpv's internal
// playlist is the queue; nothing is kept in Go.
package player

import (
	"context"
	"encoding/json"
	"fmt"
)

// Commander is the part of the mpv client the player needs.
type Commander interface {
	Command(ctx context.Context, args ...any) (json.RawMessage, error)
}

// Player controls playback.
type Player struct{ c Commander }

// New returns a Player that sends commands to c.
func New(c Commander) *Player { return &Player{c: c} }

func (p *Player) getInt(ctx context.Context, prop string) (int, error) {
	raw, err := p.c.Command(ctx, "get_property", prop)
	if err != nil {
		return 0, fmt.Errorf("get %s: %w", prop, err)
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, fmt.Errorf("get %s: %w", prop, err)
	}
	return int(f), nil
}

func (p *Player) getBool(ctx context.Context, prop string) (bool, error) {
	raw, err := p.c.Command(ctx, "get_property", prop)
	if err != nil {
		return false, fmt.Errorf("get %s: %w", prop, err)
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, fmt.Errorf("get %s: %w", prop, err)
	}
	return b, nil
}

func (p *Player) do(ctx context.Context, args ...any) error {
	if _, err := p.c.Command(ctx, args...); err != nil {
		return fmt.Errorf("%v: %w", args[0], err)
	}
	return nil
}

// Queue appends url to the playlist and starts it if mpv was idle.
func (p *Player) Queue(ctx context.Context, url string) error {
	if err := p.do(ctx, "loadfile", url, "append"); err != nil {
		return err
	}
	idle, err := p.getBool(ctx, "idle-active")
	if err != nil {
		return err
	}
	if !idle {
		return nil
	}
	count, err := p.getInt(ctx, "playlist-count")
	if err != nil {
		return err
	}
	return p.do(ctx, "playlist-play-index", count-1)
}

// PlayNow plays url immediately, keeping the rest of the queue after it.
func (p *Player) PlayNow(ctx context.Context, url string) error {
	idle, err := p.getBool(ctx, "idle-active")
	if err != nil {
		return err
	}
	if idle {
		return p.Queue(ctx, url)
	}
	count, err := p.getInt(ctx, "playlist-count")
	if err != nil {
		return err
	}
	if count == 0 {
		return p.Queue(ctx, url)
	}
	pos, err := p.getInt(ctx, "playlist-pos")
	if err != nil {
		return err
	}
	if pos < 0 {
		return p.Queue(ctx, url)
	}
	if err := p.do(ctx, "loadfile", url, "append"); err != nil {
		return err
	}
	// The new entry has index count. Move it right behind the current one.
	// NOTE: when the current entry is last, the new entry is already there,
	// so the move is skipped (moving an entry onto itself is pointless).
	if pos+1 < count {
		if err := p.do(ctx, "playlist-move", count, pos+1); err != nil {
			return err
		}
	}
	return p.do(ctx, "playlist-play-index", pos+1)
}

// Skip jumps to the next entry (at the last one this stops playback).
func (p *Player) Skip(ctx context.Context) error {
	return p.do(ctx, "playlist-next", "force")
}

// SeekPercent seeks to n percent (0..100) of the current track.
func (p *Player) SeekPercent(ctx context.Context, n int) error {
	return p.do(ctx, "seek", n, "absolute-percent")
}

// TogglePause pauses or resumes.
func (p *Player) TogglePause(ctx context.Context) error {
	return p.do(ctx, "cycle", "pause")
}

// Remove deletes the playlist entry at index.
func (p *Player) Remove(ctx context.Context, index int) error {
	return p.do(ctx, "playlist-remove", index)
}

// MoveUp swaps the entry at index with the one before it.
func (p *Player) MoveUp(ctx context.Context, index int) error {
	return p.do(ctx, "playlist-move", index, index-1)
}

// MoveDown swaps the entry at index with the one after it. playlist-move
// inserts *before* the target index, hence index+2.
func (p *Player) MoveDown(ctx context.Context, index int) error {
	return p.do(ctx, "playlist-move", index, index+2)
}

// SetPause pauses (true) or resumes (false) explicitly, unlike TogglePause.
func (p *Player) SetPause(ctx context.Context, paused bool) error {
	return p.do(ctx, "set_property", "pause", paused)
}

// Stop stops playback; the playlist is kept.
func (p *Player) Stop(ctx context.Context) error {
	return p.do(ctx, "stop")
}

// QueueMany appends all urls in order. The first one goes through Queue, so
// playback starts if mpv had gone idle; the rest are plain appends.
func (p *Player) QueueMany(ctx context.Context, urls []string) error {
	for i, u := range urls {
		if i == 0 {
			if err := p.Queue(ctx, u); err != nil {
				return err
			}
			continue
		}
		if err := p.do(ctx, "loadfile", u, "append"); err != nil {
			return err
		}
	}
	return nil
}

// ClearQueue removes every entry except the one that is playing now.
func (p *Player) ClearQueue(ctx context.Context) error {
	return p.do(ctx, "playlist-clear")
}
