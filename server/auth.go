// Registration, login and the token that proves a request is somebody's
// (02 Architecture/Backend Services.md).
//
//	POST /api/auth/register  {email, password, displayName} -> {token, user}
//	POST /api/auth/login     {email, password}              -> {token, user}
//	GET  /api/profile                                       -> {user}
//
// Email and password, bcrypt, a JWT. No OAuth: it adds provider setup and
// redirect handling for no thesis value, and it is the first thing to add if
// time turns out to be abundant.
//
// **Offline play is never gated on any of this.** Training, local versus and
// versus AI require no account (03 Game Design/Game Modes.md, D62), which is
// also why the whole package degrades to 503 rather than refusing to start when
// there is no database.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	// The cost the design note fixes. It is a deliberate ~250 ms per login on
	// modest hardware: the whole point of the parameter is to be slow, and at
	// single-digit concurrency there is nothing for it to be slow against.
	bcryptCost = 12

	// **Passwords are bounded at 72 bytes because bcrypt silently truncates
	// there.** A user who set a 100-character passphrase would be logging in
	// with the first 72 and would never be told; rejecting is the honest half
	// of that, and the limit is on *bytes* rather than characters because that
	// is what bcrypt counts.
	minPassword = 8
	maxPassword = 72

	// A token outlives a play session without outliving the evening. There is
	// no refresh token: the design does not ask for one, and the cost of not
	// having it is a login screen once a day rather than a second credential
	// with its own storage, rotation and revocation story.
	tokenLife = 24 * time.Hour

	// A request body that is not a few hundred bytes is not one of these.
	maxBody = 4 << 10
)

// Display names are shown to other players and appear on a leaderboard, so the
// set of characters is the small one rather than whatever arrived.
var displayNameOK = regexp.MustCompile(`^[A-Za-z0-9_-]{3,24}$`)

// auth holds what the handlers need: the store, and the key tokens are signed
// with. One value rather than package globals, so a test can stand up a second
// one without the two fighting over the same secret.
type auth struct {
	users  store
	secret []byte
	// Nil in the handler tests, which do not touch a match. The result endpoint
	// is registered only when it is set, for the same reason the whole package
	// is registered only when there is a database.
	matches *matchStore
}

// jwtSecret is read from the environment, or generated.
//
// **There is no default secret and there must never be one**: a signing key in
// source is a key every deployment shares, and anyone who has read the
// repository can mint a token for any account. A generated one is the
// development case and says so loudly — every token dies when the process
// restarts, which is survivable locally and would be obvious in production.
func jwtSecret(fromEnv string) []byte {
	if fromEnv != "" {
		return []byte(fromEnv)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// The process cannot sign anything without this, and carrying on with
		// a predictable key is the one outcome worse than not starting.
		log.Fatalf("no UNIVERSUS_JWT_SECRET and no system randomness: %v", err)
	}
	log.Printf("UNIVERSUS_JWT_SECRET is unset: signing with a random key (%s…), "+
		"so every session ends when this process does", hex.EncodeToString(key[:4]))
	return key
}

// routes registers the endpoints. Nothing is registered when there is no
// database: an endpoint that exists and always fails is worse than one that
// does not exist, and the client asks for a 404 exactly as it would ask for a
// server that is only a room.
func (a *auth) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/register", a.register)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("GET /api/profile", a.requireUser(a.profile))
	if a.matches != nil {
		mux.HandleFunc("POST /api/match/{id}/result", a.requireUser(a.submitResult))
	}
}

type credentials struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}

type session struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

func (a *auth) register(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, &in) {
		return
	}

	email, err := normalizeEmail(in.Email)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := checkPassword(in.Password); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(in.DisplayName)
	if !displayNameOK.MatchString(name) {
		httpError(w, http.StatusBadRequest,
			"display name must be 3-24 characters of letters, digits, _ or -")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcryptCost)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not hash the password")
		return
	}

	u, err := a.users.createUser(r.Context(), email, string(hash), name)
	if errors.Is(err, errTaken) {
		// Which of the two is taken is deliberately not said. It is an account
		// enumeration oracle either way, and "one of these is taken" is enough
		// to act on.
		httpError(w, http.StatusConflict, "that email or display name is already registered")
		return
	}
	if err != nil {
		log.Printf("register: %v", err)
		httpError(w, http.StatusInternalServerError, "could not create the account")
		return
	}

	a.issue(w, u)
}

// dummyHash is compared against when the email is unknown, so that a login for
// an account that does not exist costs the same as one for an account that
// does. Without it the response time is an oracle for which emails are
// registered, which is a list worth having if you intend to guess passwords.
//
// Generated once at startup from a value nobody can log in with.
var dummyHash = mustHash("not-a-password-this-is-a-timing-placeholder")

func mustHash(s string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(s), bcryptCost)
	if err != nil {
		log.Fatalf("bcrypt is unusable: %v", err)
	}
	return h
}

func (a *auth) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, &in) {
		return
	}

	email, err := normalizeEmail(in.Email)
	if err != nil {
		// Same answer as a wrong password: a malformed email that is rejected
		// differently is still an answer about the account.
		httpError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}

	u, hash, err := a.users.credentials(r.Context(), email)
	if err != nil && !errors.Is(err, errNoUser) {
		log.Printf("login: %v", err)
		httpError(w, http.StatusInternalServerError, "could not read the account")
		return
	}
	known := err == nil
	if !known {
		hash = string(dummyHash)
	}

	bcryptOK := bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) == nil

	// Both conditions folded into one branch without short-circuiting, so an
	// unknown email takes the same path as a known one with a wrong password.
	if subtle.ConstantTimeByteEq(b2i(known), 1)&subtle.ConstantTimeByteEq(b2i(bcryptOK), 1) != 1 {
		httpError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}

	a.issue(w, u)
}

func b2i(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// profile is the account and its ladder standing. The rating is here rather
// than on an endpoint of its own because it is the only thing anybody wants
// alongside the name, and a second request for two numbers is a second request.
func (a *auth) profile(w http.ResponseWriter, r *http.Request, u User) {
	writeJSON(w, http.StatusOK, map[string]any{
		"user":   u,
		"rating": a.users.rating(r.Context(), u.ID),
	})
}

// issue signs a token for u and writes the session.
func (a *auth) issue(w http.ResponseWriter, u User) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(u.ID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(tokenLife)),
	})
	signed, err := token.SignedString(a.secret)
	if err != nil {
		log.Printf("signing: %v", err)
		httpError(w, http.StatusInternalServerError, "could not issue a token")
		return
	}
	writeJSON(w, http.StatusOK, session{Token: signed, User: u})
}

// requireUser is the middleware: a bearer token, verified, resolved to the user
// it names. Handlers behind it never see a request that is not somebody's.
func (a *auth) requireUser(h func(http.ResponseWriter, *http.Request, User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := a.userID(r.Header.Get("Authorization"))
		if err != nil {
			httpError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		u, err := a.users.userByID(r.Context(), id)
		if err != nil {
			// A token for an account that has since been deleted is not an
			// error worth explaining; it is a request to sign in again.
			httpError(w, http.StatusUnauthorized, "sign in first")
			return
		}
		h(w, r, u)
	}
}

func (a *auth) userID(header string) (int64, error) {
	raw, found := strings.CutPrefix(header, "Bearer ")
	if !found {
		return 0, errors.New("no bearer token")
	}
	return a.subjectOf(raw)
}

// userOf resolves a bare token — no Bearer prefix — to the account it names.
// The WebSocket endpoints use it: a browser cannot set headers on a socket, so
// the token arrives as the first frame instead (see hub.accept).
func (a *auth) userOf(token string) (User, error) {
	id, err := a.subjectOf(token)
	if err != nil {
		return User{}, err
	}
	return a.users.userByID(context.Background(), id)
}

func (a *auth) subjectOf(raw string) (int64, error) {
	// **The algorithm is pinned.** Accepting whatever the token says it was
	// signed with is how `alg: none` and the HMAC-verified-against-an-RSA-key
	// trick both work; the parser enforces it rather than the caller checking
	// afterwards.
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(strings.TrimSpace(raw), claims,
		func(*jwt.Token) (any, error) { return a.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired())
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(claims.Subject, 10, 64)
}

// normalizeEmail lowercases and trims, which is what makes the unique index
// mean what people assume it means.
//
// ponytail: the shape check is one @ with something either side and a dot in
// the domain. Anything stricter is a regex nobody can read that still accepts
// addresses nobody can receive mail at; the real check is sending to it, and
// there is no mail in this project.
func normalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) < 3 || len(email) > 254 {
		return "", errors.New("email must be 3-254 characters")
	}
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" || !strings.Contains(domain, ".") ||
		strings.Contains(domain, "@") || strings.ContainsAny(email, " \t\r\n") {
		return "", errors.New("that does not look like an email address")
	}
	return email, nil
}

func checkPassword(p string) error {
	if len(p) < minPassword {
		return errors.New("password must be at least 8 characters")
	}
	if len(p) > maxPassword {
		// Said rather than truncated: see maxPassword.
		return errors.New("password must be at most 72 bytes, which is bcrypt's limit")
	}
	return nil
}

// readJSON decodes a bounded, strict body. Reports whether the handler should
// carry on; it has already answered the request when it says no.
func readJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	return decodeBody(w, r, into, maxBody)
}

// readBigJSON is the same for a match upload, which carries an input log and is
// three orders of magnitude larger than a set of credentials.
func readBigJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	return decodeBody(w, r, into, maxUpload)
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any, limit int64) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	// Unknown fields are refused: a client sending `passwrod` should be told,
	// not silently registered with an empty password it thinks it set.
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		httpError(w, http.StatusBadRequest, "expected a JSON object")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("writing a response: %v", err)
	}
}

func httpError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
