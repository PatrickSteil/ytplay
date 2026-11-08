package cast

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/mdns"
)

// Device is a cast device on the network.
type Device struct {
	Name  string // the name it was given ("Wohnzimmer")
	Model string // "Google Home Mini", "Chromecast"
	ID    string
	Addr  string // host:port to connect to
}

// lookup asks the network for cast devices. A variable so tests can answer.
var lookup = func(ctx context.Context, timeout time.Duration) ([]*mdns.ServiceEntry, error) {
	found := make(chan *mdns.ServiceEntry, 32)
	var entries []*mdns.ServiceEntry
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range found {
			entries = append(entries, e)
		}
	}()
	params := mdns.DefaultParams("_googlecast._tcp")
	params.Entries = found
	params.Timeout = timeout
	params.DisableIPv6 = true
	params.Logger = log.New(io.Discard, "", 0) // the program draws on the terminal: no log lines
	err := mdns.QueryContext(ctx, params)
	close(found)
	<-done
	return entries, err
}

func field(fields []string, key string) string {
	for _, f := range fields {
		if v, ok := strings.CutPrefix(f, key+"="); ok {
			return v
		}
	}
	return ""
}

// Discover lists the cast devices that answer within timeout, by name.
func Discover(ctx context.Context, timeout time.Duration) ([]Device, error) {
	entries, err := lookup(ctx, timeout)
	if err != nil && len(entries) == 0 {
		return nil, fmt.Errorf("look for speakers: %w", err)
	}
	seen := map[string]bool{}
	var out []Device
	for _, e := range entries {
		ip := e.AddrV4
		if ip == nil {
			continue // answered over IPv6 only: the IPv4 answer of the same device is the one used
		}
		d := Device{Name: field(e.InfoFields, "fn"), Model: field(e.InfoFields, "md"), ID: field(e.InfoFields, "id"), Addr: net.JoinHostPort(ip.String(), fmt.Sprint(e.Port))}
		if d.Name == "" || seen[d.Addr] {
			continue
		}
		seen[d.Addr] = true
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Find picks the device a user means by name: the whole name, or else a part of
// it that only one device has (case does not matter).
func Find(devices []Device, name string) (Device, error) {
	q := strings.ToLower(strings.TrimSpace(name))
	if q == "" {
		return Device{}, errors.New("which speaker?")
	}
	var partial []Device
	for _, d := range devices {
		n := strings.ToLower(d.Name)
		if n == q {
			return d, nil
		}
		if strings.Contains(n, q) {
			partial = append(partial, d)
		}
	}
	switch len(partial) {
	case 0:
		return Device{}, fmt.Errorf("no speaker called %q", name)
	case 1:
		return partial[0], nil
	}
	var names []string
	for _, d := range partial {
		names = append(names, d.Name)
	}
	return Device{}, fmt.Errorf("%q could be %s", name, strings.Join(names, " or "))
}
