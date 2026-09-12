// Universus backend. Today it is one thing: the room that pairs two clients,
// carries their signalling blobs, and then carries their input packets if
// WebRTC could not connect (02 Architecture/Transport and Connectivity.md).
//
//	GET /ws?room=CODE
//
// The first client to ask for a code waits; the second is paired with it. Each
// is then told which it is — "host" or "guest", one text frame — and from that
// point the server copies every message to the other end and reads none of
// them.
//
// Signalling and the relay fallback are therefore the same code path: the pair
// trade SDP over this socket, try P2P for five seconds, and keep the socket as
// the transport if ICE never connects. A server that parsed the traffic would
// have to know which phase the match is in; one that copies bytes does not.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// Generous for a base64 SDP blob with a long candidate list, mean for
	// anything else. An input packet is ~20 bytes.
	readLimit = 32 << 10

	// A peer whose socket has stopped draining must not wedge the other end's
	// read loop. Dropping the message would be wrong here: unlike the data
	// channel, the relay is the only copy of an input packet in flight.
	writeTimeout = 5 * time.Second
)

// Room codes are typed by hand and appear in a URL, so they are checked rather
// than trusted.
var codeOK = regexp.MustCompile(`^[A-Za-z0-9-]{1,32}$`)

type client struct {
	conn *websocket.Conn
	// Carries the peer to a client that had to wait for one. Buffered, so the
	// joiner never blocks on a waiter that is busy.
	paired chan *websocket.Conn
}

type rooms struct {
	mu      sync.Mutex
	waiting map[string]*client
}

// join registers c under code and returns the client already waiting there, if
// there was one — in which case the code is freed again, since the pair now
// talk to each other and not to the room.
//
// ponytail: a code holds one waiting client, not a two-seat room. A third
// joiner waits for a fourth instead of being refused. Fix when the backend
// issues match IDs and hands out seats with them (M3, Backend Services).
func (r *rooms) join(code string, c *client) *client {
	r.mu.Lock()
	defer r.mu.Unlock()

	if other, ok := r.waiting[code]; ok {
		delete(r.waiting, code)
		return other
	}
	r.waiting[code] = c
	return nil
}

func (r *rooms) leave(code string, c *client) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Only if it is still us: by now the code may hold somebody else.
	if r.waiting[code] == c {
		delete(r.waiting, code)
	}
}

func (r *rooms) serve(w http.ResponseWriter, req *http.Request) {
	code := req.URL.Query().Get("room")
	if !codeOK.MatchString(code) {
		http.Error(w, "room code must be 1-32 characters of [A-Za-z0-9-]", http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
		// Input packets are 20 bytes of bitfields and do not compress; agreeing
		// on compression costs handshake and per-message work for nothing.
		CompressionMode: websocket.CompressionDisabled,
		// ponytail: no cookies and no auth here, so a forged origin gains
		// nothing. Tighten when auth lands (M3).
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return // Accept has already written the failure
	}
	defer conn.CloseNow()
	conn.SetReadLimit(readLimit)

	me := &client{conn: conn, paired: make(chan *websocket.Conn, 1)}

	// Resolves the peer once, whether the pairing happened on this goroutine or
	// arrived while we were waiting for somebody to join. It has to be checked
	// on the way out too, not only per message: a host that closes its tab
	// before sending the offer has never read anything, and a peer left unread
	// is a guest nobody ever hangs up on.
	var peer *websocket.Conn
	peerOf := func() *websocket.Conn {
		if peer == nil {
			select {
			case p := <-me.paired:
				peer = p
			default:
			}
		}
		return peer
	}

	// Hanging up on the peer is what tells the other end the match is over.
	// After a successful P2P connection this socket is already dead weight, so
	// closing it costs a match in progress nothing.
	defer func() {
		if p := peerOf(); p != nil {
			p.CloseNow()
		}
	}()

	if other := r.join(code, me); other != nil {
		// Roles go out before either end can forward anything, so this is the
		// first frame each reads and "you are the host" doubles as "the other
		// end is here, start offering".
		if tell(other.conn, "host") != nil || tell(conn, "guest") != nil {
			return
		}
		peer = other.conn
		other.paired <- conn
	} else {
		defer r.leave(code, me)
	}

	// Not req.Context(): reads end when the socket does, which is the only
	// signal that matters here.
	ctx := context.Background()

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}

		p := peerOf()
		if p == nil {
			continue // nobody to copy to yet, so this was noise
		}

		if err := write(p, typ, data); err != nil {
			return
		}
	}
}

func tell(conn *websocket.Conn, role string) error {
	return write(conn, websocket.MessageText, []byte(role))
}

func write(conn *websocket.Conn, typ websocket.MessageType, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return conn.Write(ctx, typ, data)
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	r := &rooms{waiting: map[string]*client{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", r.serve)

	log.Printf("relay listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
