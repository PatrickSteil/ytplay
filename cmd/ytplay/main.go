// Command ytplay is a small terminal YouTube music player.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/PatrickSteil/ytplay/internal/analyze"
	"github.com/PatrickSteil/ytplay/internal/autodj"
	"github.com/PatrickSteil/ytplay/internal/cast"
	"github.com/PatrickSteil/ytplay/internal/cover"
	"github.com/PatrickSteil/ytplay/internal/mpris"
	"github.com/PatrickSteil/ytplay/internal/mpv"
	"github.com/PatrickSteil/ytplay/internal/player"
	"github.com/PatrickSteil/ytplay/internal/ui"
	"github.com/PatrickSteil/ytplay/internal/yt"
)

func main() {
	os.Exit(run())
}

func run() int {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: ytplay [-version]\n\nA terminal YouTube music player. Requires mpv and yt-dlp.")
	}
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(versionString())
		return 0
	}

	// Startup checks happen before the TUI starts.
	mpvPath, err := exec.LookPath("mpv")
	if err != nil {
		fmt.Fprintln(os.Stderr, "ytplay: mpv not found. install mpv: sudo apt install mpv / brew install mpv")
		return 1
	}
	ytdlp, err := exec.LookPath("yt-dlp")
	if err != nil {
		fmt.Fprintln(os.Stderr, "ytplay: yt-dlp not found. install yt-dlp: pipx install yt-dlp / brew install yt-dlp")
		return 1
	}
	// LookPath may return a relative path if PATH contains one; mpv needs an absolute one.
	if abs, err := filepath.Abs(ytdlp); err == nil {
		ytdlp = abs
	}

	return runTUI(ytdlp, mpvPath)
}

func setupLogging() func() {
	if os.Getenv("YTPLAY_DEBUG") != "1" {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
		return func() {}
	}
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "ytplay-debug.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return func() {}
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return func() { f.Close() }
}

func runTUI(ytdlp, mpvPath string) int {
	defer setupLogging()()
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ytplay: config:", err)
		return 1
	}

	if ct := os.Getenv("COLORTERM"); ct != "truecolor" && ct != "24bit" {
		fmt.Fprintln(os.Stderr, "ytplay: warning: COLORTERM is not truecolor/24bit; the cover image will look wrong")
	}

	pl, err := player.New(context.Background(), func(ctx context.Context, name string) (player.Deck, error) {
		c, err := mpv.Start(ctx, ytdlp, name)
		if err != nil {
			return nil, err
		}
		if err := c.ObserveAll(); err != nil {
			_ = c.Close()
			return nil, err
		}
		return c, nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "ytplay:", err)
		return 1
	}
	defer pl.Close()
	var publish func(ui.NowPlaying)
	stop := make(chan struct{})
	if srv, err := mpris.Start(mprisControls{pl}, func() time.Duration { return playerPosition(pl) }); err != nil {
		slog.Debug("MPRIS disabled", "err", err) // no session bus: carry on without it
	} else {
		defer srv.Close()
		publish = startPublisher(srv, stop)
	}

	// Full analyses (DJ mode) share half the cores, however many songs are in flight.
	pool := analyze.NewPool(max(1, runtime.NumCPU()/2))
	covers := cover.NewCache()
	m := ui.New(ui.Deps{
		Publish:      publish,
		Cover:        covers.Get,
		Autoplay:     cfg.autoplay,
		SaveAutoplay: cfg.saveAutoplay,
		Devices: func(ctx context.Context) ([]string, error) {
			ds, err := cast.Discover(ctx, 3*time.Second)
			names := make([]string, len(ds))
			for i, d := range ds {
				names[i] = d.Name
			}
			return names, err
		},
		Cast: func(ctx context.Context, name string) (string, error) {
			ds, err := cast.Discover(ctx, 3*time.Second)
			if err != nil {
				return "", err
			}
			dev, err := cast.Find(ds, name)
			if err != nil {
				return "", err
			}
			client, err := cast.Dial(ctx, dev.Addr)
			if err != nil {
				return "", err
			}
			deck := player.NewCastDeck(dev.Name, client, func(ctx context.Context, songURL string) (string, string, error) {
				return yt.StreamURL(ctx, ytdlp, songURL)
			})
			if err := pl.CastTo(ctx, dev.Name, deck); err != nil {
				_ = deck.Close()
				return "", err
			}
			return dev.Name, nil
		},
		CastOff: pl.CastOff,
		SetTrim: func(songs func(url string) (autodj.Song, bool)) { pl.SetTrim(songs) },
		Copy:    func(text string) error { return copyToClipboard(text, os.Stdout) },
		Search: func(ctx context.Context, q string) ([]yt.Result, map[yt.Source]error, error) {
			return yt.SearchAll(ctx, ytdlp, q, 10)
		},
		Info: func(ctx context.Context, id string, comments int) (yt.Info, error) {
			return yt.FetchInfo(ctx, ytdlp, id, comments)
		},
		Analyze: func(ctx context.Context, id string, duration float64, level analyze.Level) (analyze.Result, error) {
			o := analyze.Options{Level: level}
			if level == analyze.Full {
				o.Pool = pool // light stays on one core
			}
			res, err := analyze.SongWith(ctx, mpvPath, ytdlp, id, duration, o)
			if err != nil && ctx.Err() == nil {
				slog.Debug("analysis failed", "id", id, "duration", duration, "level", level, "err", err)
			}
			return res, err
		},
		Load: func(ctx context.Context, link yt.Link) (yt.Loaded, error) {
			return yt.LoadLink(ctx, ytdlp, link, yt.MaxPlaylist)
		},
		Similar: func(ctx context.Context, seeds []string, exclude map[string]bool, n int) ([]yt.Result, error) {
			return yt.Similar(ctx, ytdlp, seeds, exclude, n)
		},
		SetAutoDJ: func(on bool, songs func(url string) (autodj.Song, bool)) {
			pl.SetAutoDJ(on, songs)
		},
		Download: func(ctx context.Context, id, path string) error {
			return yt.Download(ctx, ytdlp, id, path)
		},
		Player:   pl,
		Position: pl.Position,
	})
	p := tea.NewProgram(m, tea.WithAltScreen())

	var quitting, mpvDied atomic.Bool
	go func() { // forward mpv events to the UI
		for {
			select {
			case ev := <-pl.Events():
				slog.Debug("mpv event", "name", ev.Name, "prop", ev.Property, "reason", ev.Reason, "data", string(ev.Data))
				p.Send(ui.MpvEventMsg{Event: ev})
			case <-stop:
				return
			}
		}
	}()
	go func() { // mpv died on its own?
		select {
		case <-pl.Done():
			if !quitting.Load() {
				mpvDied.Store(true)
				p.Send(ui.MpvExitedMsg{})
			}
		case <-stop:
		}
	}()

	// Closing the terminal sends SIGHUP, which would kill us without cleanup.
	// (Bubble Tea itself handles SIGINT and SIGTERM.)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
			p.Quit()
		case <-stop:
		}
	}()

	_, runErr := p.Run()
	quitting.Store(true)
	close(stop)
	tail := strings.TrimSpace(pl.StderrTail())
	pl.Close()

	if mpvDied.Load() {
		fmt.Fprintf(os.Stderr, "mpv exited: %s\n", tail)
		return 1
	}
	if runErr != nil && !errors.Is(runErr, tea.ErrProgramKilled) {
		fmt.Fprintln(os.Stderr, "ytplay:", runErr)
		return 1
	}
	return 0
}

// mprisControls maps the desktop shell's buttons to player actions.
type mprisControls struct{ p *player.Player }

func (c mprisControls) do(f func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return f(ctx)
}
func (c mprisControls) Play() error {
	return c.do(func(ctx context.Context) error { return c.p.SetPause(ctx, false) })
}
func (c mprisControls) Pause() error {
	return c.do(func(ctx context.Context) error { return c.p.SetPause(ctx, true) })
}
func (c mprisControls) PlayPause() error {
	return c.do(c.p.TogglePause)
}
func (c mprisControls) Next() error { return c.do(c.p.Skip) }
func (c mprisControls) Stop() error { return c.do(c.p.Stop) }

// playerPosition is the current playback position (0 when nothing plays).
func playerPosition(p *player.Player) time.Duration {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	elapsed, _, err := p.Position(ctx)
	if err != nil {
		return 0
	}
	return time.Duration(elapsed * float64(time.Second))
}

// startPublisher returns a non-blocking publish function for the UI. A single
// goroutine applies only the latest snapshot, so a slow D-Bus call can never
// stall the UI and updates can never be applied out of order.
func startPublisher(srv *mpris.Server, stop <-chan struct{}) func(ui.NowPlaying) {
	var mu sync.Mutex
	var latest ui.NowPlaying
	wake := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-wake:
				mu.Lock()
				np := latest
				mu.Unlock()
				srv.Update(mpris.State{
					Active: np.Active, ID: np.ID, Title: np.Title, Channel: np.Channel,
					Duration: np.Duration, Paused: np.Paused, HasNext: np.HasNext,
				})
			case <-stop:
				return
			}
		}
	}()
	return func(np ui.NowPlaying) {
		mu.Lock()
		latest = np
		mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}
