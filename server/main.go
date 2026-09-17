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
	"os"
	"regexp"
	"sync"
	"sync/atomic"
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

	// How long a client has to send its opening token frame. Generous for a
	// slow connection, short enough that a socket opened and abandoned is not a
	// goroutine for the rest of the day.
	handshakeTimeout = 10 * time.Second
)

// hub is the server's shared state: the codes people type at each other, the
// queues people wait in, and the accounts both are gated on when there are any.
type hub struct {
	rooms *rooms
	queue *matchmaker
	// nil when the server runs without a database, which is also when it runs
	// without accounts.
	auth *auth
}

// Room codes are typed by hand and appear in a URL, so they are checked rather
// than trusted.
var codeOK = regexp.MustCompile(`^[A-Za-z0-9-]{1,32}$`)

type client struct {
	conn *websocket.Conn
	// Carries the peer to a client that had to wait for one. Buffered, so the
	// joiner never blocks on a waiter that is busy.
	paired chan *websocket.Conn

	// Who this is, or nil when the server is running without accounts. The
	// room and the relay never read it; matchmaking is the whole reason it
	// exists, since a queue has to know whose ladder rating it is sorting.
	user *User
	// Ladder points, read once when the queue is joined. A snapshot on purpose:
	// a rating that changed mid-queue would move somebody between bands while
	// they waited, which is a fairness argument nobody asked to have.
	lp int
	// Round trip to *this server* in nanoseconds, which is the only latency
	// anybody can measure before a match exists (02 Architecture/Backend
	// Services.md). A proxy for the one that matters, and honest to describe as
	// such.
	//
	// Atomic because it is measured on its own goroutine while the matchmaker
	// reads it: a WebSocket ping is only answered while somebody is reading the
	// socket, so the measurement has to run *beside* the relay loop rather than
	// before it. Zero means unmeasured, which the bands read as no evidence
	// rather than as a fast connection.
	rtt atomic.Int64
}

func newClient(conn *websocket.Conn) *client {
	return &client{conn: conn, paired: make(chan *websocket.Conn, 1)}
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

// accept upgrades the request and reads the client's opening frame, which is
// its token or an empty string.
//
// **The token is a frame and not a query parameter.** A browser cannot set
// headers on a WebSocket, so the usual alternative is `?token=…`, and a URL is
// the one place a credential is guaranteed to be written down — proxy logs,
// server logs, browser history, the Referer of anything the page loads. One
// frame costs a round trip nobody notices.
//
// Every client sends the frame whether or not it has a token, so there is no
// negotiation about which protocol is in force: a server without accounts
// ignores it, and one with accounts refuses an empty one.
func (s *hub) accept(w http.ResponseWriter, req *http.Request) (*websocket.Conn, *User, bool) {
	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
		// Input packets are 20 bytes of bitfields and do not compress; agreeing
		// on compression costs handshake and per-message work for nothing.
		CompressionMode: websocket.CompressionDisabled,
		// The credential is a frame rather than a cookie, so a page on another
		// origin has nothing to borrow by opening this socket.
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return nil, nil, false // Accept has already written the failure
	}
	conn.SetReadLimit(readLimit)

	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()

	typ, data, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageText {
		conn.Close(websocket.StatusPolicyViolation, "expected a token frame first")
		return nil, nil, false
	}

	if s.auth == nil {
		return conn, nil, true // no accounts on this server: nothing to check
	}

	u, err := s.auth.userOf(string(data))
	if err != nil {
		// D104: a private match by code needs an account exactly as a ranked one
		// does. One identity path online, or two to keep honest.
		conn.Close(websocket.StatusPolicyViolation, "sign in first")
		return nil, nil, false
	}
	return conn, &u, true
}

func (s *hub) serveRoom(w http.ResponseWriter, req *http.Request) {
	code := req.URL.Query().Get("room")
	if !codeOK.MatchString(code) {
		http.Error(w, "room code must be 1-32 characters of [A-Za-z0-9-]", http.StatusBadRequest)
		return
	}

	conn, user, ok := s.accept(w, req)
	if !ok {
		return
	}
	defer conn.CloseNow()

	me := newClient(conn)
	me.user = user

	if other := s.rooms.join(code, me); other != nil {
		if !pair(other, me) {
			return
		}
	} else {
		defer s.rooms.leave(code, me)
	}
	relay(conn, me)
}

// pair introduces two clients to each other: roles out first, then each is
// handed the other's socket.
//
// Roles go out before either end can forward anything, so this is the first
// frame each reads and "you are the host" doubles as "the other end is here,
// start offering". The host also decides the transport and the characters
// (D88, D106), which is why which end is which has to be settled here rather
// than raced for.
func pair(host, guest *client) bool {
	if tell(host.conn, "host") != nil || tell(guest.conn, "guest") != nil {
		return false
	}
	host.paired <- guest.conn
	guest.paired <- host.conn
	return true
}

// relay copies every frame to the peer and reads none of them. It is the whole
// of the server's part in a match: signalling and the relay fallback are the
// same code path, because a server that parsed the traffic would have to know
// which phase the match is in and one that copies bytes does not.
func relay(conn *websocket.Conn, me *client) {
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
	static := flag.String("static", "", "directory of built client to serve at /; empty serves signalling only")
	flag.Parse()

	h := &hub{rooms: &rooms{waiting: map[string]*client{}}, queue: newMatchmaker()}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.serveRoom)

	// **The database is optional and the server starts without one.** Training,
	// local versus and versus AI require no account at all (03 Game Design/Game
	// Modes.md), and the room and the relay read nothing from Postgres — so a
	// database that is down costs the ladder rather than the demo. It is also
	// what lets the whole thing run from one binary with no arguments.
	ctx := context.Background()
	pool, err := openDB(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	if pool != nil {
		defer pool.Close()
		// Migrations run at startup, in the binary that needs them: a container
		// that starts is a container that has its schema (02 Architecture/
		// Database Schema.md).
		n, err := migrate(ctx, pool, migrationFiles)
		if err != nil {
			log.Fatalf("migrating: %v", err)
		}
		log.Printf("database ready, %d migration(s) applied", n)

		h.auth = &auth{users: pgStore{pool: pool}, secret: jwtSecret(os.Getenv("UNIVERSUS_JWT_SECRET"))}
		h.auth.routes(mux)

		// Matchmaking exists only where accounts do: a queue is an ordering of
		// ladder ratings, and there are none without a database (D104).
		mux.HandleFunc("/ws/queue", h.serveQueue)
		go h.queue.run(ctx)

		log.Printf("accounts and matchmaking enabled")
	} else {
		log.Printf("no DATABASE_URL: offline modes only, no accounts, no queue")
	}

	if *static != "" {
		// One origin for the page and the socket, which is what a remote session needs: a
		// guest on https cannot open a ws:// socket to a separate port, and a tunnel or a
		// host publishes one port. ponytail: a plain FileServer, no SPA fallback — the
		// client is one page and carries its state in query parameters.
		mux.Handle("/", http.FileServer(http.Dir(*static)))
		log.Printf("serving %s at /", *static)
	}

	log.Printf("relay listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
