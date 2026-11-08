package cast

import (
	"bytes"
	"testing"
)

func TestAMessageSurvivesTheWire(t *testing.T) {
	m := message{Source: "sender-0", Dest: "receiver-0", Namespace: nsConnection, Payload: `{"type":"CONNECT"}`}
	var buf bytes.Buffer
	if err := writeMessage(&buf, m); err != nil {
		t.Fatal(err)
	}
	got, err := readMessage(&buf)
	if err != nil || got != m {
		t.Errorf("got %+v err %v, want %+v", got, err, m)
	}
}

// The bytes a real CastMessage has: protocol_version 0, then the strings, payload_type 0.
func TestTheWireFormatIsACastMessageProtobuf(t *testing.T) {
	var buf bytes.Buffer
	_ = writeMessage(&buf, message{Source: "a", Dest: "b", Namespace: "c", Payload: "d"})
	want := []byte{0, 0, 0, 16, 8, 0, 18, 1, 'a', 26, 1, 'b', 34, 1, 'c', 40, 0, 50, 1, 'd'}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("bytes %v, want %v", buf.Bytes(), want)
	}
}

func TestLongPayloadsUseALongLength(t *testing.T) {
	long := string(bytes.Repeat([]byte("x"), 300))
	var buf bytes.Buffer
	_ = writeMessage(&buf, message{Source: "s", Dest: "d", Namespace: "n", Payload: long})
	got, err := readMessage(&buf)
	if err != nil || got.Payload != long {
		t.Errorf("err %v, payload %d bytes", err, len(got.Payload))
	}
}

func TestBrokenInputIsAnErrorNotAPanic(t *testing.T) {
	for _, in := range [][]byte{nil, {0, 0, 0}, {0, 0, 0, 5, 1, 2}, {0, 0, 0, 3, 18, 200, 1}, {0xff, 0xff, 0xff, 0xff}} {
		if _, err := readMessage(bytes.NewReader(in)); err == nil {
			t.Errorf("%v was accepted", in)
		}
	}
}
