package cast

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/hashicorp/mdns"
)

func entry(name string, ip string, fields ...string) *mdns.ServiceEntry {
	return &mdns.ServiceEntry{Name: name, AddrV4: net.ParseIP(ip), Port: 8009, InfoFields: fields}
}

func withLookup(t *testing.T, entries []*mdns.ServiceEntry, err error) {
	t.Helper()
	old := lookup
	lookup = func(ctx context.Context, timeout time.Duration) ([]*mdns.ServiceEntry, error) { return entries, err }
	t.Cleanup(func() { lookup = old })
}

func TestDevicesAreReadFromTheirAnnouncements(t *testing.T) {
	withLookup(t, []*mdns.ServiceEntry{
		entry("Google-Home-Mini-16d0._googlecast._tcp.local.", "192.168.178.44", "id=16d0", "fn=Wohnzimmer", "md=Google Home Mini", "ve=05"),
		entry("Chromecast-9a._googlecast._tcp.local.", "192.168.178.50", "id=9a", "fn=TV", "md=Chromecast"),
	}, nil)
	got, err := Discover(bg, time.Second)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v err %v", got, err)
	}
	want := Device{Name: "TV", Model: "Chromecast", ID: "9a", Addr: "192.168.178.50:8009"} // sorted by name
	if got[0] != want {
		t.Errorf("first %+v, want %+v", got[0], want)
	}
	if got[1].Name != "Wohnzimmer" || got[1].Model != "Google Home Mini" || got[1].Addr != "192.168.178.44:8009" {
		t.Errorf("second %+v", got[1])
	}
}

func TestADeviceSeenTwiceIsListedOnce(t *testing.T) {
	// the same device answers on IPv4 and IPv6, and the IPv6 answer has no IPv4 address
	v6 := &mdns.ServiceEntry{Name: "x", AddrV6IPAddr: &net.IPAddr{IP: net.ParseIP("fe80::1")}, Port: 8009, InfoFields: []string{"id=1", "fn=Kitchen"}}
	withLookup(t, []*mdns.ServiceEntry{v6, entry("x", "192.168.178.47", "id=1", "fn=Kitchen"), entry("x", "192.168.178.47", "id=1", "fn=Kitchen")}, nil)
	got, _ := Discover(bg, time.Second)
	if len(got) != 1 || got[0].Addr != "192.168.178.47:8009" {
		t.Errorf("got %+v", got)
	}
}

func TestAnAnnouncementWithoutANameOrAnAddressIsSkipped(t *testing.T) {
	withLookup(t, []*mdns.ServiceEntry{entry("a", "192.168.178.1", "id=1"), entry("b", "", "id=2", "fn=Nowhere"), entry("c", "192.168.178.3", "id=3", "fn=Good")}, nil)
	got, _ := Discover(bg, time.Second)
	if len(got) != 1 || got[0].Name != "Good" {
		t.Errorf("got %+v", got)
	}
}

func TestDiscoveryFailureIsReported(t *testing.T) {
	withLookup(t, nil, errors.New("no multicast"))
	if _, err := Discover(bg, time.Second); err == nil {
		t.Error("no error")
	}
}

func TestFindByName(t *testing.T) {
	ds := []Device{{Name: "Wohnzimmer", ID: "1"}, {Name: "Kitchen speaker", ID: "2"}, {Name: "Kitchen TV", ID: "3"}}
	for q, want := range map[string]string{"wohnzimmer": "1", "WOHN": "1", "kitchen speaker": "2", "speaker": "2"} {
		d, err := Find(ds, q)
		if err != nil || d.ID != want {
			t.Errorf("Find(%q) = %+v, %v, want %s", q, d, err, want)
		}
	}
	for _, q := range []string{"kitchen", "", "garage"} {
		if _, err := Find(ds, q); err == nil {
			t.Errorf("Find(%q) succeeded", q)
		}
	}
}
