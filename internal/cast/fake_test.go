package cast

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeDevice is a cast receiver: it speaks the protocol well enough for the
// default media receiver, over TLS with a self-signed certificate like a real one.
type fakeDevice struct {
	t    *testing.T
	ln   net.Listener
	mu   sync.Mutex
	got  []sent // every message the client sent
	conn net.Conn
	// behaviour
	loadFails   bool
	otherApp    bool // another app is running too, with its own transport
	noTransport bool // a LAUNCH that never gets a transport id
	state       string
	pos, dur    float64
	volume      float64
	muted       bool
	sessionID   int
}

type sent struct {
	Dest, Namespace string
	Payload         map[string]any
}

func (s sent) kind() string { k, _ := s.Payload["type"].(string); return k }

func newFakeDevice(t *testing.T) *fakeDevice {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake cast"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDevice{t: t, ln: ln, state: "IDLE", volume: 0.5, dur: 200}
	t.Cleanup(func() {
		ln.Close()
		d.mu.Lock()
		if d.conn != nil {
			d.conn.Close()
		}
		d.mu.Unlock()
	})
	go d.serve()
	return d
}

func (d *fakeDevice) addr() string { return d.ln.Addr().String() }

func (d *fakeDevice) serve() {
	conn, err := d.ln.Accept()
	if err != nil {
		return
	}
	d.mu.Lock()
	d.conn = conn
	d.mu.Unlock()
	for {
		m, err := readMessage(conn)
		if err != nil {
			return
		}
		var p map[string]any
		_ = json.Unmarshal([]byte(m.Payload), &p)
		d.mu.Lock()
		d.got = append(d.got, sent{m.Dest, m.Namespace, p})
		d.mu.Unlock()
		d.handle(conn, m, p)
	}
}

func (d *fakeDevice) reply(conn net.Conn, src, ns string, payload map[string]any) {
	b, _ := json.Marshal(payload)
	_ = writeMessage(conn, message{Source: src, Dest: "sender-0", Namespace: ns, Payload: string(b)})
}

func (d *fakeDevice) mediaStatus(conn net.Conn, reqID any, reason string) {
	st := map[string]any{"mediaSessionId": d.sessionID, "playerState": d.state, "currentTime": d.pos, "playbackRate": 1,
		"media": map[string]any{"duration": d.dur}}
	if reason != "" {
		st["idleReason"] = reason
	}
	d.reply(conn, "tr-1", nsMedia, map[string]any{"type": "MEDIA_STATUS", "requestId": reqID, "status": []any{st}})
}

func (d *fakeDevice) receiverStatus(conn net.Conn, reqID any) {
	apps := []any{}
	if !d.noTransport && d.launched() {
		apps = append(apps, map[string]any{"appId": defaultReceiver, "transportId": "tr-1", "sessionId": "s-1", "displayName": "Default Media Receiver"})
	}
	if d.otherApp { // listed after ours: taking the last app would be wrong
		apps = append(apps, map[string]any{"appId": "233637DE", "transportId": "other-9", "sessionId": "s-9"})
	}
	d.reply(conn, "receiver-0", nsReceiver, map[string]any{"type": "RECEIVER_STATUS", "requestId": reqID,
		"status": map[string]any{"applications": apps, "volume": map[string]any{"level": d.volume, "muted": d.muted}}})
}

func (d *fakeDevice) launched() bool {
	for _, g := range d.got {
		if g.kind() == "LAUNCH" {
			return true
		}
	}
	return false
}

func (d *fakeDevice) handle(conn net.Conn, m message, p map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	req := p["requestId"]
	switch p["type"] {
	case "PING":
		d.reply(conn, m.Dest, nsHeartbeat, map[string]any{"type": "PONG"})
	case "GET_STATUS":
		if m.Namespace == nsReceiver {
			d.receiverStatus(conn, req)
		} else {
			d.mediaStatus(conn, req, "")
		}
	case "LAUNCH":
		d.receiverStatus(conn, req)
	case "SET_VOLUME":
		v, _ := p["volume"].(map[string]any)
		if l, ok := v["level"].(float64); ok {
			d.volume = l
		}
		if mu, ok := v["muted"].(bool); ok {
			d.muted = mu
		}
		d.receiverStatus(conn, req)
	case "LOAD":
		if d.loadFails {
			d.reply(conn, "tr-1", nsMedia, map[string]any{"type": "LOAD_FAILED", "requestId": req})
			return
		}
		d.sessionID++
		d.state, d.pos = "PLAYING", 0
		d.mediaStatus(conn, req, "")
	case "PAUSE":
		d.state = "PAUSED"
		d.mediaStatus(conn, req, "")
	case "PLAY":
		d.state = "PLAYING"
		d.mediaStatus(conn, req, "")
	case "SEEK":
		d.pos, _ = p["currentTime"].(float64)
		d.mediaStatus(conn, req, "")
	case "STOP":
		d.state = "IDLE"
		d.mediaStatus(conn, req, "CANCELLED")
	}
}

// finish makes the song end by itself.
func (d *fakeDevice) finish(reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.state = "IDLE"
	d.mediaStatus(d.conn, 0, reason)
}

// sentKinds are the types of what the client sent so far.
func (d *fakeDevice) sentKinds() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, g := range d.got {
		out = append(out, g.Namespace[len("urn:x-cast:com.google.cast."):]+":"+g.kind())
	}
	return out
}

func (d *fakeDevice) last(kind string) (sent, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := len(d.got) - 1; i >= 0; i-- {
		if d.got[i].kind() == kind {
			return d.got[i], true
		}
	}
	return sent{}, false
}
