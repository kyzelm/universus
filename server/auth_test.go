package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// fakeStore is the second implementation the store interface exists for. Auth
// is the code most worth testing and the code least worth needing a live
// Postgres to test.
type fakeStore struct {
	mu     sync.Mutex
	byID   map[int64]User
	hashes map[string]string // email -> password hash
	lp     map[int64]int     // ladder points, which only the queue reads
	next   int64
	fail   error // forced, for the "the database is on fire" paths
}

func newStore() *fakeStore {
	return &fakeStore{byID: map[int64]User{}, hashes: map[string]string{}, lp: map[int64]int{}, next: 1}
}

func (f *fakeStore) createUser(_ context.Context, email, hash, name string) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return User{}, f.fail
	}
	for _, u := range f.byID {
		if u.Email == email || u.DisplayName == name {
			return User{}, errTaken
		}
	}
	u := User{ID: f.next, Email: email, DisplayName: name}
	f.next++
	f.byID[u.ID] = u
	f.hashes[email] = hash
	return u, nil
}

func (f *fakeStore) credentials(_ context.Context, email string) (User, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return User{}, "", f.fail
	}
	for _, u := range f.byID {
		if u.Email == email {
			return u, f.hashes[email], nil
		}
	}
	return User{}, "", errNoUser
}

func (f *fakeStore) ratingOf(_ context.Context, id int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lp[id]
}

func (f *fakeStore) userByID(_ context.Context, id int64) (User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.byID[id]; ok {
		return u, nil
	}
	return User{}, errNoUser
}

func testAuth() (*auth, *http.ServeMux) {
	a := &auth{users: newStore(), secret: []byte("test-secret")}
	mux := http.NewServeMux()
	a.routes(mux)
	return a, mux
}

func post(t *testing.T, mux *http.ServeMux, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func decodeSession(t *testing.T, w *httptest.ResponseRecorder) session {
	t.Helper()
	var s session
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatalf("decoding %q: %v", w.Body.String(), err)
	}
	return s
}

const goodRegister = `{"email":"Player@Example.COM ","password":"correct horse","displayName":"player-one"}`

func TestRegisterThenLogIn(t *testing.T) {
	_, mux := testAuth()

	w := post(t, mux, "/api/auth/register", goodRegister)
	if w.Code != http.StatusOK {
		t.Fatalf("register: %d %s", w.Code, w.Body)
	}
	got := decodeSession(t, w)
	// Trimmed and lowercased on the way in, or the unique index does not mean
	// what everybody assumes it means.
	if got.User.Email != "player@example.com" {
		t.Errorf("stored email %q, want it normalized", got.User.Email)
	}
	if got.Token == "" {
		t.Error("registered with no token: the client would have to log in again immediately")
	}

	w = post(t, mux, "/api/auth/login",
		`{"email":"player@example.com","password":"correct horse"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	if decodeSession(t, w).User.ID != got.User.ID {
		t.Error("logged in as somebody else")
	}
}

// **A password is never echoed and a hash never leaves the server.** The check
// is on the raw response rather than on the struct, because the struct is
// exactly what somebody in a hurry would add a field to.
func TestNoSecretsInAnyResponse(t *testing.T) {
	_, mux := testAuth()

	for _, w := range []*httptest.ResponseRecorder{
		post(t, mux, "/api/auth/register", goodRegister),
		post(t, mux, "/api/auth/login", `{"email":"player@example.com","password":"correct horse"}`),
	} {
		body := w.Body.String()
		for _, secret := range []string{"correct horse", "$2a$", "$2b$", "password_hash"} {
			if strings.Contains(body, secret) {
				t.Errorf("a response contained %q: %s", secret, body)
			}
		}
	}
}

func TestRegistrationRefusesWhatItShould(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want int
	}{
		{"no @", `{"email":"player","password":"correct horse","displayName":"player-one"}`, 400},
		{"no dot in the domain", `{"email":"a@b","password":"correct horse","displayName":"p1x"}`, 400},
		{"short password", `{"email":"a@b.com","password":"short","displayName":"player-one"}`, 400},
		// bcrypt silently truncates at 72 bytes, so a longer one would be a
		// passphrase the user never actually set.
		{"past bcrypt's limit", `{"email":"a@b.com","password":"` + strings.Repeat("x", 73) +
			`","displayName":"player-one"}`, 400},
		{"display name too short", `{"email":"a@b.com","password":"correct horse","displayName":"ab"}`, 400},
		{"display name with spaces", `{"email":"a@b.com","password":"correct horse","displayName":"a b c"}`, 400},
		{"a typo'd field", `{"email":"a@b.com","passwrod":"correct horse","displayName":"player-one"}`, 400},
		{"not JSON at all", `nope`, 400},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, mux := testAuth()
			if w := post(t, mux, "/api/auth/register", c.body); w.Code != c.want {
				t.Errorf("got %d, want %d: %s", w.Code, c.want, w.Body)
			}
		})
	}
}

// Which of the two is taken is deliberately not said: either answer is an
// account enumeration oracle.
func TestASecondRegistrationIsRefusedWithoutSayingWhich(t *testing.T) {
	_, mux := testAuth()
	post(t, mux, "/api/auth/register", goodRegister)

	// The taken email and the taken display name must be the *same* answer,
	// byte for byte. Either one identified on its own is a way to ask whether
	// an address is registered.
	takenEmail := post(t, mux, "/api/auth/register", goodRegister)
	takenName := post(t, mux, "/api/auth/register",
		`{"email":"other@example.com","password":"correct horse","displayName":"player-one"}`)

	for _, w := range []*httptest.ResponseRecorder{takenEmail, takenName} {
		if w.Code != http.StatusConflict {
			t.Fatalf("got %d, want 409: %s", w.Code, w.Body)
		}
	}
	if takenEmail.Body.String() != takenName.Body.String() {
		t.Errorf("two answers name which field was taken: %q against %q", takenEmail.Body, takenName.Body)
	}
}

// An unknown email and a wrong password must be the same answer, or the
// response is a list of which accounts exist.
func TestAnUnknownEmailLooksExactlyLikeAWrongPassword(t *testing.T) {
	_, mux := testAuth()
	post(t, mux, "/api/auth/register", goodRegister)

	unknown := post(t, mux, "/api/auth/login", `{"email":"nobody@example.com","password":"correct horse"}`)
	wrong := post(t, mux, "/api/auth/login", `{"email":"player@example.com","password":"wrong horse"}`)
	malformed := post(t, mux, "/api/auth/login", `{"email":"not-an-email","password":"correct horse"}`)

	for _, w := range []*httptest.ResponseRecorder{unknown, wrong, malformed} {
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("got %d, want 401: %s", w.Code, w.Body)
		}
	}
	if unknown.Body.String() != wrong.Body.String() || wrong.Body.String() != malformed.Body.String() {
		t.Errorf("three different answers: %q, %q, %q", unknown.Body, wrong.Body, malformed.Body)
	}
}

func TestProfileNeedsARealToken(t *testing.T) {
	a, mux := testAuth()
	token := decodeSession(t, post(t, mux, "/api/auth/register", goodRegister)).Token

	get := func(header string) int {
		req := httptest.NewRequest("GET", "/api/profile", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code
	}

	if code := get("Bearer " + token); code != http.StatusOK {
		t.Errorf("a real token got %d", code)
	}
	for _, header := range []string{
		"",
		token,                   // no Bearer prefix
		"Bearer " + token + "x", // signature no longer matches
		"Bearer not.a.token",
	} {
		if code := get(header); code != http.StatusUnauthorized {
			t.Errorf("%q got %d, want 401", header, code)
		}
	}

	// A token signed with a different key is the attack this is all for.
	other := &auth{users: a.users, secret: []byte("a-different-secret")}
	forged, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString(other.secret)
	if err != nil {
		t.Fatal(err)
	}
	if code := get("Bearer " + forged); code != http.StatusUnauthorized {
		t.Errorf("a token signed with another key got %d", code)
	}
}

// `alg: none` is the oldest JWT attack there is, and the only defence is
// pinning the algorithm at the parser rather than checking after the fact.
func TestAnUnsignedTokenIsRefused(t *testing.T) {
	a, _ := testAuth()

	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Subject:   "1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.userID("Bearer " + unsigned); err == nil {
		t.Error("an unsigned token was accepted")
	}
}

func TestAnExpiredTokenIsRefused(t *testing.T) {
	a, _ := testAuth()

	stale, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
	}).SignedString(a.secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.userID("Bearer " + stale); err == nil {
		t.Error("an expired token was accepted")
	}

	// And one with no expiry at all, which is a token that never stops working.
	forever, err := jwt.NewWithClaims(jwt.SigningMethodHS256,
		jwt.RegisteredClaims{Subject: "1"}).SignedString(a.secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.userID("Bearer " + forever); err == nil {
		t.Error("a token with no expiry was accepted")
	}
}

// The hash actually protects the password: the stored value is not the password
// and does verify against it.
func TestThePasswordIsStoredAsABcryptHash(t *testing.T) {
	a, mux := testAuth()
	post(t, mux, "/api/auth/register", goodRegister)

	_, hash, err := a.users.credentials(context.Background(), "player@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "correct horse" || !strings.HasPrefix(hash, "$2") {
		t.Fatalf("stored %q, want a bcrypt hash", hash)
	}
	if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != bcryptCost {
		t.Errorf("cost %d (err %v), want %d", cost, err, bcryptCost)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("correct horse")); err != nil {
		t.Errorf("the stored hash does not verify against the password: %v", err)
	}
}

// A signing key in source is a key every deployment shares. There must be no
// default, and an absent one must not be a fixed value.
func TestTheSigningKeyIsNeverAConstant(t *testing.T) {
	if got := string(jwtSecret("from-the-environment")); got != "from-the-environment" {
		t.Errorf("the environment was ignored, got %q", got)
	}

	a, b := jwtSecret(""), jwtSecret("")
	if string(a) == string(b) {
		t.Error("two generated keys are identical: it is a constant in disguise")
	}
	if len(a) < 32 {
		t.Errorf("a generated key is %d bytes, want at least 32", len(a))
	}
}
