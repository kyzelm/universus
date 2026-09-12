package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func relay(t *testing.T) string {
	t.Helper()

	r := &rooms{waiting: map[string]*client{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", r.serve)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/ws?room="
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func read(t *testing.T, conn *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return typ, data
}

func send(t *testing.T, conn *websocket.Conn, typ websocket.MessageType, data []byte) {
	t.Helper()

	if err := write(conn, typ, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// The whole contract in one test: two clients on one code learn which is which,
// and from then on whatever one sends the other reads, message type intact.
func TestPairAndForward(t *testing.T) {
	url := relay(t) + "abc-1"

	host := dial(t, url)
	guest := dial(t, url)

	if _, role := read(t, host); string(role) != "host" {
		t.Errorf("first in the room got role %q, want host", role)
	}
	if _, role := read(t, guest); string(role) != "guest" {
		t.Errorf("second in the room got role %q, want guest", role)
	}

	// An offer blob: text, and the host sends it because it was told it is one.
	send(t, host, websocket.MessageText, []byte("offer-blob"))
	typ, data := read(t, guest)
	if typ != websocket.MessageText || string(data) != "offer-blob" {
		t.Errorf("guest read (%v, %q), want (text, offer-blob)", typ, data)
	}

	// An input packet on the way back: binary, and unchanged. The relay is the
	// transport once ICE has failed, so the bytes the sim reads come through here.
	packet := []byte{1, 0, 0, 0, 7, 2, 0x11, 0x00, 0x22, 0x00}
	send(t, guest, websocket.MessageBinary, packet)
	typ, data = read(t, host)
	if typ != websocket.MessageBinary || string(data) != string(packet) {
		t.Errorf("host read (%v, % x), want (binary, % x)", typ, data, packet)
	}
}

// Separate codes are separate matches, which is what makes a private match by
// code work at all.
func TestCodesDoNotMix(t *testing.T) {
	base := relay(t)

	a := dial(t, base+"room-a")
	b := dial(t, base+"room-b")

	// Neither is paired, so neither has been told a role. A leaked message would
	// arrive well inside this window.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	send(t, a, websocket.MessageText, []byte("offer-blob"))
	if _, data, err := b.Read(ctx); err == nil {
		t.Errorf("room-b read %q from room-a", data)
	}
}

// Hanging up ends the match for both ends: the survivor's read fails instead of
// waiting for input that is never coming.
func TestPeerLeaving(t *testing.T) {
	url := relay(t) + "abc-1"

	host := dial(t, url)
	guest := dial(t, url)
	read(t, host)
	read(t, guest)

	host.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, data, err := guest.Read(ctx); err == nil {
		t.Errorf("guest read %q after the host left, want an error", data)
	}
}

func TestBadCode(t *testing.T) {
	base := relay(t)

	for _, code := range []string{"", "with space", "zażółć", "../etc", "a-very-long-code-that-runs-past-thirty-two"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		conn, resp, err := websocket.Dial(ctx, base+code, nil)
		cancel()

		if err == nil {
			conn.CloseNow()
			t.Errorf("code %q was accepted", code)
			continue
		}
		if resp == nil || resp.StatusCode != http.StatusBadRequest {
			t.Errorf("code %q: %v, want 400", code, err)
		}
	}
}
