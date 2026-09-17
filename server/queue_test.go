package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func at(lp int, waitedFor time.Duration, now time.Time) *waiting {
	return &waiting{c: &client{lp: lp}, joined: now.Add(-waitedFor)}
}

// The table from the design note, read back. It is the whole of the policy, so
// a typo in it is the policy.
func TestTheBandsAreTheOnesTheDesignSpecifies(t *testing.T) {
	for _, c := range []struct {
		waited  time.Duration
		ranked  int
		casual  int
		maxPing time.Duration
	}{
		{0, 100, 500, 60 * time.Millisecond},
		{14 * time.Second, 100, 500, 60 * time.Millisecond},
		{15 * time.Second, 250, 1500, 100 * time.Millisecond},
		{30 * time.Second, 500, unlimited, 150 * time.Millisecond},
		{61 * time.Second, unlimited, unlimited, 0},
	} {
		lp, ping := bandFor("ranked", c.waited)
		if lp != c.ranked {
			t.Errorf("ranked at %v: %d LP, want %d", c.waited, lp, c.ranked)
		}
		if c.maxPing != 0 && ping != c.maxPing {
			t.Errorf("at %v: ping %v, want %v", c.waited, ping, c.maxPing)
		}
		if lp, _ := bandFor("casual", c.waited); lp != c.casual {
			t.Errorf("casual at %v: %d LP, want %d", c.waited, lp, c.casual)
		}
	}

	// Casual widens faster than ranked at every step, which is the difference
	// between the two modes and not a coincidence of the numbers.
	for _, waited := range []time.Duration{0, 15 * time.Second, 30 * time.Second} {
		r, _ := bandFor("ranked", waited)
		c, _ := bandFor("casual", waited)
		if c <= r {
			t.Errorf("at %v casual is %d and ranked is %d: casual must widen faster", waited, c, r)
		}
	}
}

func TestABandWidensUntilItMatches(t *testing.T) {
	now := time.Now()
	far := []*waiting{at(0, 0, now), at(400, 0, now)}

	if acceptable("ranked", now, far[0], far[1]) {
		t.Error("400 LP apart matched immediately in ranked")
	}
	// The longer wait decides, so one patient player is enough.
	patient := at(400, 31*time.Second, now)
	if !acceptable("ranked", now, far[0], patient) {
		t.Error("still refused after 30 seconds, when the band is ±500")
	}

	// And everything matches eventually, which is the property that matters
	// with a handful of testers: a band that never opened fully is a queue that
	// never matches.
	if !acceptable("ranked", now, at(0, 0, now), at(99999, 61*time.Second, now)) {
		t.Error("a minute in, the band is still refusing somebody")
	}
}

// The ping band is a hint made of two round trips to this server, and an
// unmeasured one is not evidence of anything.
func TestThePingBandRefusesAndThenStopsRefusing(t *testing.T) {
	now := time.Now()
	slow := func(waited time.Duration) *waiting {
		w := at(0, waited, now)
		w.c.rtt.Store(int64(80 * time.Millisecond))
		return w
	}

	if acceptable("casual", now, slow(0), slow(0)) {
		t.Error("160 ms of estimated ping matched inside the 60 ms band")
	}
	if !acceptable("casual", now, slow(61*time.Second), slow(0)) {
		t.Error("the ping band never opened")
	}

	unmeasured := at(0, 0, now)
	if !acceptable("casual", now, unmeasured, slow(0)) {
		t.Error("a client whose ping never came back was refused for it")
	}
}

// Oldest first. Pairing the closest pair overall is defensible for fairness and
// indefensible for whoever sits between two clusters — the point of a widening
// band is that waiting eventually works.
func TestTheLongestWaiterIsServedFirst(t *testing.T) {
	now := time.Now()
	m := newMatchmaker()

	patient := at(1000, 90*time.Second, now)
	near := at(1010, 0, now)
	alsoNear := at(1005, 0, now)
	m.modes["ranked"] = []*waiting{near, alsoNear, patient}

	a, b := m.bestPair("ranked", now)
	if a != patient {
		t.Fatalf("paired %v first, want the player who waited 90 seconds", a)
	}
	// …and against the closest opponent that player can have.
	if b != alsoNear {
		t.Errorf("matched against %d LP, want the closest at %d", b.c.lp, alsoNear.c.lp)
	}
}

func TestAnEmptyOrSingleQueuePairsNobody(t *testing.T) {
	m := newMatchmaker()
	if a, _ := m.bestPair("ranked", time.Now()); a != nil {
		t.Error("an empty queue produced a pair")
	}
	m.modes["ranked"] = []*waiting{at(0, 0, time.Now())}
	if a, _ := m.bestPair("ranked", time.Now()); a != nil {
		t.Error("one player in a queue produced a pair")
	}
}

func TestLeavingTheQueueIsSafeTwice(t *testing.T) {
	m := newMatchmaker()
	w := &waiting{c: &client{}, joined: time.Now()}
	m.modes["casual"] = []*waiting{w}

	m.leave("casual", w)
	m.leave("casual", w) // a socket that died after being matched does this
	if len(m.modes["casual"]) != 0 {
		t.Errorf("%d still queued", len(m.modes["casual"]))
	}
}

// --- the endpoints ---------------------------------------------------------

// authedHub is a server with accounts, backed by the same fake store the auth
// tests use.
func authedHub(t *testing.T) (*hub, string) {
	t.Helper()
	h := &hub{
		rooms: &rooms{waiting: map[string]*client{}},
		queue: newMatchmaker(),
		auth:  &auth{users: newStore(), secret: []byte("test-secret")},
	}
	return h, serverFor(t, h)
}

func tokenFor(t *testing.T, h *hub, email, name string) string {
	t.Helper()

	mux := http.NewServeMux()
	h.auth.routes(mux)
	w := post(t, mux, "/api/auth/register",
		`{"email":"`+email+`","password":"correct horse","displayName":"`+name+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	return decodeSession(t, w).Token
}

// **D104: a private match by code needs an account exactly as a ranked one
// does.** One identity path online, or two to keep honest.
func TestBothSocketsRefuseAnUnsignedClient(t *testing.T) {
	h, base := authedHub(t)
	token := tokenFor(t, h, "player@example.com", "player-one")

	for _, url := range []string{base + "/ws?room=abc-1", base + "/ws/queue?mode=casual"} {
		conn := dialAs(t, url, "not-a-token")

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _, err := conn.Read(ctx)
		cancel()
		if err == nil {
			t.Errorf("%s accepted a client with no account", url)
		}

		// And the same socket with a real token is not refused, so the check is
		// the token rather than the endpoint being broken.
		ok := dialAs(t, url, token)
		ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
		_, _, err = ok.Read(ctx)
		cancel()
		if err != nil && !strings.Contains(err.Error(), "context deadline") {
			t.Errorf("%s refused a signed-in client: %v", url, err)
		}
	}
}

// Two people queueing is the whole of matchmaking at this scale, and it must
// not wait for a sweep to notice.
func TestTwoInAQueueArePairedAtOnce(t *testing.T) {
	h, base := authedHub(t)
	a := tokenFor(t, h, "a@example.com", "player-a")
	b := tokenFor(t, h, "b@example.com", "player-b")

	first := dialAs(t, base+"/ws/queue?mode=casual", a)
	// Queued before the second dials, or which of them is the host is a race
	// rather than the order they arrived in.
	waitFor(t, func() bool { return h.queue.size("casual") == 1 })
	second := dialAs(t, base+"/ws/queue?mode=casual", b)

	if _, role := read(t, first); string(role) != "host" {
		t.Errorf("first in the queue got %q, want host", role)
	}
	if _, role := read(t, second); string(role) != "guest" {
		t.Errorf("second in the queue got %q, want guest", role)
	}

	// And then it is a room: the same socket carries the signalling.
	send(t, first, websocket.MessageText, []byte("offer-blob"))
	if _, data := read(t, second); string(data) != "offer-blob" {
		t.Errorf("the guest read %q", data)
	}
}

// The ping band needs a number to work with, and the number is only ever
// produced while somebody is reading the socket — the bug this test exists to
// catch is a ping that is sent before the read loop and times out measuring
// nothing, which looks from outside exactly like a fast connection.
func TestTheRoundTripIsActuallyMeasured(t *testing.T) {
	h, base := authedHub(t)
	conn := dialAs(t, base+"/ws/queue?mode=casual", tokenFor(t, h, "a@example.com", "player-a"))

	// A pong is only sent by an end that is reading. A browser answers pings in
	// the protocol layer whether or not the page is reading; this client has to
	// be told to, which is a fact about the test harness and not about the
	// server.
	go func() {
		for {
			if _, _, err := conn.Read(context.Background()); err != nil {
				return
			}
		}
	}()

	waitFor(t, func() bool { return h.queue.size("casual") == 1 })
	waitFor(t, func() bool {
		h.queue.mu.Lock()
		defer h.queue.mu.Unlock()
		return h.queue.modes["casual"][0].c.rtt.Load() > 0
	})
}

func TestTheQueueRefusesAModeItDoesNotHave(t *testing.T) {
	_, base := authedHub(t)

	for _, mode := range []string{"", "training", "ranked-plus"} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		conn, resp, err := websocket.Dial(ctx, base+"/ws/queue?mode="+mode, nil)
		cancel()
		if err == nil {
			conn.CloseNow()
			t.Errorf("mode %q was accepted", mode)
			continue
		}
		if resp == nil || resp.StatusCode != http.StatusBadRequest {
			t.Errorf("mode %q: %v, want 400", mode, err)
		}
	}
}

// A player who closes the tab has to leave the queue, or the next arrival is
// paired with a socket nobody is reading.
func TestLeavingTheTabLeavesTheQueue(t *testing.T) {
	h, base := authedHub(t)
	a := tokenFor(t, h, "a@example.com", "player-a")

	conn := dialAs(t, base+"/ws/queue?mode=ranked", a)
	waitFor(t, func() bool { return h.queue.size("ranked") == 1 })

	conn.CloseNow()
	waitFor(t, func() bool { return h.queue.size("ranked") == 0 })
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the queue to settle")
}

var _ = httptest.NewServer
