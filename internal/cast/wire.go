// Package cast speaks enough of the Chromecast protocol (CASTV2) to play audio
// URLs on a Chromecast or Google Home speaker with its built-in media player:
// TLS to port 8009, messages that are protobuf CastMessages with a length in
// front, carrying JSON.
package cast

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	nsConnection    = "urn:x-cast:com.google.cast.tp.connection"
	nsHeartbeat     = "urn:x-cast:com.google.cast.tp.heartbeat"
	nsReceiver      = "urn:x-cast:com.google.cast.receiver"
	nsMedia         = "urn:x-cast:com.google.cast.media"
	defaultReceiver = "CC1AD845" // the Default Media Receiver, on every cast device
	maxMessage      = 1 << 20
)

// message is a CastMessage: who it is from and for, which channel, and the
// JSON payload.
type message struct {
	Source, Dest, Namespace, Payload string
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendString(b []byte, field int, s string) []byte {
	b = appendVarint(b, uint64(field<<3|2))
	b = appendVarint(b, uint64(len(s)))
	return append(b, s...)
}

// writeMessage sends m: a 4 byte big-endian length, then the protobuf.
func writeMessage(w io.Writer, m message) error {
	body := appendVarint(nil, 1<<3|0) // protocol_version
	body = appendVarint(body, 0)      // CASTV2_1_0
	body = appendString(body, 2, m.Source)
	body = appendString(body, 3, m.Dest)
	body = appendString(body, 4, m.Namespace)
	body = appendVarint(body, 5<<3|0) // payload_type
	body = appendVarint(body, 0)      // STRING
	body = appendString(body, 6, m.Payload)
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	_, err := w.Write(append(frame, body...))
	return err
}

func readVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errors.New("cast: bad varint")
}

// readMessage reads one message.
func readMessage(r io.Reader) (message, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return message{}, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 || n > maxMessage {
		return message{}, fmt.Errorf("cast: message of %d bytes", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return message{}, err
	}
	var m message
	for len(body) > 0 {
		tag, k, err := readVarint(body)
		if err != nil {
			return message{}, err
		}
		body = body[k:]
		switch field, wire := tag>>3, tag&7; wire {
		case 0:
			_, k, err := readVarint(body)
			if err != nil {
				return message{}, err
			}
			body = body[k:]
		case 2:
			l, k, err := readVarint(body)
			if err != nil || uint64(len(body)-k) < l {
				return message{}, errors.New("cast: bad field length")
			}
			s := string(body[k : k+int(l)])
			body = body[k+int(l):]
			switch field {
			case 2:
				m.Source = s
			case 3:
				m.Dest = s
			case 4:
				m.Namespace = s
			case 6:
				m.Payload = s
			}
		default:
			return message{}, fmt.Errorf("cast: unsupported wire type %d", wire)
		}
	}
	return m, nil
}
