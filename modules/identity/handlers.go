package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"

	"github.com/hollis-labs/substrate/apierr"
)

const maxBody = 64 << 10

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)

func (s *Service) handlers() map[string]http.Handler {
	h := func(f http.HandlerFunc) http.Handler { return f }
	m := map[string]http.Handler{
		"GET /api/setup":                        h(s.setupStatus),
		"POST /api/setup":                       h(s.setup),
		"POST /api/login":                       h(s.login),
		"GET /api/identity/jwks":                h(s.jwksHandler),
		"POST /api/pair":                        h(s.pair),
		"POST /api/logout":                      h(s.logout),
		"PUT /api/password":                     h(s.changePassword),
		"GET /api/identity/profile":             h(s.getProfile),
		"PUT /api/identity/profile":             h(s.updateProfile),
		"POST /api/token":                       h(s.tokenHandler),
		"GET /api/identity/sessions":            h(s.sessions),
		"DELETE /api/identity/sessions/":        h(s.revokeSession),
		"GET /api/identity/users":               h(s.listUsers),
		"POST /api/identity/users":              h(s.createUser),
		"PUT /api/identity/users/":              h(s.updateUser),
		"GET /api/identity/keys":                h(s.listKeys),
		"POST /api/identity/keys":               h(s.createKey),
		"DELETE /api/identity/keys/":            h(s.revokeKey),
		"POST /api/identity/pairing-codes":      h(s.createPairingCode),
		"POST /api/identity/signing-key/rotate": h(s.rotateSigningKey),
		"GET /api/identity/signing-key":         h(s.signingKeyState),
	}
	for k, v := range s.passkeyRoutes(h) {
		m[k] = v
	}
	for k, v := range s.inviteRoutes(h) {
		m[k] = v
	}
	for k, v := range s.totpRoutes(h) {
		m[k] = v
	}
	for k, v := range s.recoverRoutes(h) {
		m[k] = v
	}
	for k, v := range s.myKeysRoutes(h) {
		m[k] = v
	}
	return m
}

// ---- small helpers ----

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		apierr.Write(w, http.StatusBadRequest, "invalid_request", "the request body is not valid", nil)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// tooMany answers 429 rate_limited with a Retry-After of the whole seconds
// (rounded up, at least 1) until the limiter lets the caller try again.
func tooMany(w http.ResponseWriter, wait time.Duration) {
	secs := int((wait + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	apierr.Write(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; wait a few minutes", nil)
}

// orEmpty turns a nil slice into an empty one so a list encodes as [] and not null.
func orEmpty[T any](l []T) []T {
	if l == nil {
		return []T{}
	}
	return l
}

func bad(w http.ResponseWriter, msg string) {
	apierr.Write(w, http.StatusBadRequest, "invalid_request", msg, nil)
}
func notFound(w http.ResponseWriter) {
	apierr.Write(w, http.StatusNotFound, "not_found", "not found", nil)
}
func failed(w http.ResponseWriter) {
	apierr.Write(w, http.StatusInternalServerError, "internal_error", "internal error", nil)
}

// caller is the principal the guard stamped.
func caller(r *http.Request) (libauth.Principal, bool) {
	p, ok := libauth.FromContext(r.Context())
	return p, ok && p.Verified
}

// callerUser is the signed-in user behind a session or token whose subject is a
// user. Service credentials have none.
func (s *Service) callerUser(r *http.Request) (User, libauth.Principal, bool) {
	p, ok := caller(r)
	if !ok || !strings.HasPrefix(p.ID, "user:") {
		return User{}, p, false
	}
	u, err := s.db.userByID(r.Context(), strings.TrimPrefix(p.ID, "user:"))
	if err != nil || u.Disabled {
		return User{}, p, false
	}
	return u, p, true
}

func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Service) isAdmin(p libauth.Principal) bool { return p.HasScope(s.app.Name + ".admin") }

// ---- setup, sign-in, sign-out ----

func (s *Service) setupStatus(w http.ResponseWriter, r *http.Request) {
	exists, err := s.OwnerExists(r.Context())
	if err != nil {
		failed(w)
		return
	}
	// setup_over_http says whether POST /api/setup can complete it; false means
	// the owner is created offline ("<app> identity setup").
	// setup_link says POST /api/setup takes a setup_token (a link "<app> identity
	// setup-link" printed), whatever identity.setup says.
	writeJSON(w, http.StatusOK, map[string]any{"setup_required": !exists, "setup_over_http": s.cfg.setupHTTP, "setup_link": true, "passkeys": s.cfg.passkeys})
}

type credentials struct {
	DisplayName string `json:"display_name"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

func (s *Service) newUser(c credentials, scopes []string) (User, error) {
	username := strings.ToLower(strings.TrimSpace(c.Username))
	if !usernameRe.MatchString(username) {
		return User{}, errors.New("username must be 2 to 64 characters: lowercase letters, digits, dot, dash or underscore")
	}
	if err := s.checkPassword(c.Password); err != nil {
		return User{}, err
	}
	name := strings.TrimSpace(c.DisplayName)
	if name == "" {
		name = username
	}
	if len(name) > 200 {
		return User{}, errors.New("display name is too long")
	}
	hash, err := s.hashPassword(c.Password)
	if err != nil {
		return User{}, err
	}
	now := time.Now().UTC()
	return User{ID: newID("usr_"), Username: username, DisplayName: name, Scopes: scopes,
		CreatedAt: now, UpdatedAt: now, passwordHash: hash, passwordChangedAt: now}, nil
}

// setup makes the owner, once, and signs them in. With a setup_token (a setup link) it
// works in either identity.setup mode; without one only under identity.setup: http.
func (s *Service) setup(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		apierr.Write(w, http.StatusForbidden, "bad_origin", "the request did not come from this app", nil)
		return
	}
	var in setupInput
	if !decode(w, r, &in) {
		return
	}
	if in.SetupToken != "" {
		s.setupWithToken(w, r, in)
		return
	}
	if !s.cfg.setupHTTP {
		apierr.Write(w, http.StatusForbidden, "setup_offline", "first-run setup over HTTP is disabled; create the owner on the host with `"+s.app.Name+" identity setup`, or open the link `"+s.app.Name+" identity setup-link` prints", nil)
		return
	}
	c := in.credentials
	u, err := s.newUser(c, []string{"*"})
	if err != nil {
		bad(w, err.Error())
		return
	}
	switch err := s.db.createFirstUser(r.Context(), u); {
	case errors.Is(err, ErrSetupClosed):
		apierr.Write(w, http.StatusConflict, "setup_closed", "setup is already done", nil)
		return
	case err != nil:
		failed(w)
		return
	}
	s.startSession(w, r, u, MethodSetup)
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		apierr.Write(w, http.StatusForbidden, "bad_origin", "the request did not come from this app", nil)
		return
	}
	var in struct {
		credentials
		Code string `json:"code"` // TOTP or recovery code, when the account has TOTP
	}
	if !decode(w, r, &in) {
		return
	}
	c := in.credentials
	username := strings.ToLower(strings.TrimSpace(c.Username))
	key := username + "|" + clientAddr(r)
	now := time.Now()
	if wait := s.limiter.wait(key, now); wait > 0 {
		tooMany(w, wait)
		return
	}
	u, err := s.db.userByUsername(r.Context(), username)
	hash := ""
	if err == nil {
		hash = u.passwordHash
	}
	if !s.verifyPassword(hash, c.Password) || err != nil || u.Disabled {
		s.limiter.fail(key, now)
		apierr.Write(w, http.StatusUnauthorized, "invalid_login", "invalid username or password", nil)
		return
	}
	if s.db.passkeyOnly(r.Context(), u.ID) || s.passkeyInsteadOfPassword(r.Context(), u.ID) {
		s.limiter.clear(key)
		apierr.Write(w, http.StatusForbidden, "passkey_required", "this account signs in with a passkey only", nil)
		return
	}
	method := MethodPassword
	if s.db.totpEnabled(r.Context(), u.ID) {
		if strings.TrimSpace(in.Code) == "" {
			// The password was right; the page asks for the code and sends both again.
			apierr.Write(w, http.StatusUnauthorized, "totp_required", "enter the code from your authenticator app", nil)
			return
		}
		if method, err = s.checkSecondStep(r.Context(), u.ID, in.Code); err != nil {
			s.limiter.fail(key, now)
			badCode(w)
			return
		}
	}
	s.limiter.clear(key)
	s.startSession(w, r, u, method)
}

// startSession creates a session, sets the cookie and answers with the user.
func (s *Service) startSession(w http.ResponseWriter, r *http.Request, u User, method string) {
	now := time.Now().UTC()
	secret := randomString(32)
	sess := Session{ID: newID("ss_"), UserAgent: truncate(r.UserAgent(), 200), CreatedAt: now, ExpiresAt: now.Add(s.cfg.sessionTTL), LastSeenAt: now, Method: method}
	if err := s.db.createSession(r.Context(), sess, u.ID, secret); err != nil {
		failed(w)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cfg.cookie, Value: sessionPrefix + secret, Path: "/", Expires: sess.ExpiresAt,
		HttpOnly: true, Secure: s.secureCookie(r), SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, s.profile(r.Context(), u))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// profile is the signed-in answer: the scopes are the effective ones (the user's own and
// their role's), which is what the guard checks.
func (s *Service) profile(ctx context.Context, u User) map[string]any {
	role, _ := s.db.roleOf(ctx, s.db.st, u.ID)
	scopes, need := effectiveScopes(s.app.Name, u.Scopes, role), s.secondFactorRequired(ctx, u.ID, role)
	if need {
		scopes = []string{}
	}
	return map[string]any{"id": u.ID, "username": u.Username, "display_name": u.DisplayName, "email": u.Email,
		"scopes": scopes, "role": role, "totp": s.db.totpEnabled(ctx, u.ID), "second_factor_required": need}
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	if p, ok := caller(r); ok && p.SessionID != "" {
		_ = s.db.revokeSession(r.Context(), p.SessionID, "", time.Now())
	}
	http.SetCookie(w, &http.Cookie{Name: s.cfg.cookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secureCookie(r), SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

// ---- password and profile ----

func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) {
	u, p, ok := s.callerUser(r)
	if !ok {
		apierr.Write(w, http.StatusForbidden, "user_required", "this needs a signed-in user", nil)
		return
	}
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	key := "pw|" + u.ID
	if wait := s.limiter.wait(key, time.Now()); wait > 0 {
		tooMany(w, wait)
		return
	}
	if !s.verifyPassword(u.passwordHash, in.Current) {
		s.limiter.fail(key, time.Now())
		apierr.Write(w, http.StatusUnauthorized, "invalid_login", "the current password is not right", nil)
		return
	}
	if err := s.checkPassword(in.New); err != nil {
		bad(w, err.Error())
		return
	}
	hash, err := s.hashPassword(in.New)
	if err != nil {
		failed(w)
		return
	}
	now := time.Now()
	if err := s.db.updateUser(r.Context(), u.ID, map[string]any{"password_hash": hash, "password_changed_at": now.Unix()}); err != nil {
		failed(w)
		return
	}
	// A new password ends every other sign-in.
	_ = s.db.revokeSessionsExcept(r.Context(), u.ID, p.SessionID, now)
	w.WriteHeader(http.StatusNoContent)
}

// assuranceLocal is the only assurance this module can vouch for: the host
// holds the credentials. "verified" is reserved for an external verifier and is
// never emitted here.
const assuranceLocal = "local"

// getProfile answers who the signed-in person is. A request with no credential
// never reaches it (the guard answers 401). A caller that is not a person (an
// API key, a minted token for a service, a disabled or removed account) gets
// 403 user_required, as every other person-only route answers.
func (s *Service) getProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := caller(r)
	if !ok || !strings.HasPrefix(p.ID, "user:") {
		apierr.Write(w, http.StatusForbidden, "user_required", "this needs a signed-in user", nil)
		return
	}
	u, err := s.db.userByID(r.Context(), strings.TrimPrefix(p.ID, "user:"))
	if err != nil || u.Disabled {
		apierr.Write(w, http.StatusForbidden, "user_required", "this needs a signed-in user", nil)
		return
	}
	prof := s.profile(r.Context(), u)
	prof["assurance"], prof["sign_in"] = assuranceLocal, s.db.sessionMethod(r.Context(), p.SessionID)
	writeJSON(w, http.StatusOK, prof)
}

func (s *Service) updateProfile(w http.ResponseWriter, r *http.Request) {
	u, _, ok := s.callerUser(r)
	if !ok {
		apierr.Write(w, http.StatusForbidden, "user_required", "this needs a signed-in user", nil)
		return
	}
	var in struct {
		DisplayName *string `json:"display_name"`
		Email       *string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	set := map[string]any{}
	if in.DisplayName != nil {
		name := strings.TrimSpace(*in.DisplayName)
		if name == "" || len(name) > 200 {
			bad(w, "display name must be 1 to 200 characters")
			return
		}
		set["display_name"] = name
	}
	if in.Email != nil {
		email := strings.TrimSpace(*in.Email)
		if len(email) > 254 || (email != "" && !strings.Contains(email, "@")) {
			bad(w, "email is not valid")
			return
		}
		set["email"] = email
	}
	if err := s.db.updateUser(r.Context(), u.ID, set); err != nil {
		failed(w)
		return
	}
	u, _ = s.db.userByID(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, s.profile(r.Context(), u))
}

// ---- sessions ----

func (s *Service) sessions(w http.ResponseWriter, r *http.Request) {
	u, p, ok := s.callerUser(r)
	if !ok {
		apierr.Write(w, http.StatusForbidden, "user_required", "this needs a signed-in user", nil)
		return
	}
	list, err := s.db.listSessions(r.Context(), u.ID, time.Now())
	if err != nil {
		failed(w)
		return
	}
	for i := range list {
		list[i].Current = list[i].ID == p.SessionID
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": orEmpty(list)})
}

func (s *Service) revokeSession(w http.ResponseWriter, r *http.Request) {
	u, p, ok := s.callerUser(r)
	if !ok {
		apierr.Write(w, http.StatusForbidden, "user_required", "this needs a signed-in user", nil)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/identity/sessions/")
	owner := u.ID
	if s.isAdmin(p) {
		owner = "" // an administrator may end anyone's
	}
	if err := s.db.revokeSession(r.Context(), id, owner, time.Now()); err != nil {
		notFound(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- users (administrators) ----

func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.db.listUsers(r.Context())
	if err != nil {
		failed(w)
		return
	}
	roles, err := s.db.roles(r.Context())
	if err != nil {
		failed(w)
		return
	}
	totp := s.db.totpUsers(r.Context())
	for i := range users {
		users[i].Role = roles[users[i].ID]
		users[i].TOTP = totp[users[i].ID]
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": orEmpty(users)})
}

func (s *Service) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		credentials
		Scopes []string `json:"scopes"`
		Role   string   `json:"role"` // default "member" when no scopes are given
	}
	if !decode(w, r, &in) {
		return
	}
	p, _ := caller(r)
	if in.Role == "" && len(in.Scopes) == 0 {
		in.Role = RoleMember
	}
	if in.Role != "" && !s.mayGrantRole(p, in.Role) {
		bad(w, "give a role whose scopes the caller itself holds (owner, admin, member or guest)")
		return
	}
	// Only scopes actually asked for: grantable treats an empty list as "all the
	// caller holds", which is right for narrowing a token and wrong for a new user.
	if len(in.Scopes) > 0 {
		if _, ok := grantable(p.Scopes, in.Scopes); !ok {
			bad(w, "give scopes the caller itself holds")
			return
		}
	}
	u, err := s.newUser(in.credentials, orEmpty(in.Scopes))
	if err != nil {
		bad(w, err.Error())
		return
	}
	switch err := s.db.createUser(r.Context(), u, in.Role); {
	case errors.Is(err, ErrConflict):
		apierr.Write(w, http.StatusConflict, "exists", "that username is taken", nil)
	case err != nil:
		failed(w)
	default:
		u.Role = in.Role
		writeJSON(w, http.StatusCreated, u)
	}
}

func (s *Service) updateUser(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/identity/users/")
	var in struct {
		Disabled *bool     `json:"disabled"`
		Scopes   *[]string `json:"scopes"`
		Password *string   `json:"password"`
		Role     *string   `json:"role"`
		// ResetTOTP removes the user's TOTP and recovery codes (a lost authenticator).
		ResetTOTP bool `json:"reset_totp"`
	}
	if !decode(w, r, &in) {
		return
	}
	target, err := s.db.userByID(r.Context(), id)
	if err != nil {
		notFound(w)
		return
	}
	p, _ := caller(r)
	set := map[string]any{}
	if in.Disabled != nil {
		if *in.Disabled && p.ID == userPrincipal(target.ID) {
			bad(w, "you cannot disable yourself")
			return
		}
		d := 0
		if *in.Disabled {
			d = 1
		}
		set["disabled"] = d
	}
	if in.Scopes != nil {
		// An empty list clears the user's own scopes (their role still applies);
		// grantable would read it as "everything the caller holds".
		scopes := []string{}
		if len(*in.Scopes) > 0 {
			var ok bool
			if scopes, ok = grantable(p.Scopes, *in.Scopes); !ok {
				bad(w, "give scopes the caller itself holds")
				return
			}
		}
		set["scopes"] = strings.Join(scopes, " ")
	}
	if in.Password != nil {
		if err := s.checkPassword(*in.Password); err != nil {
			bad(w, err.Error())
			return
		}
		hash, err := s.hashPassword(*in.Password)
		if err != nil {
			failed(w)
			return
		}
		set["password_hash"], set["password_changed_at"] = hash, time.Now().Unix()
	}
	if in.ResetTOTP && p.ID == userPrincipal(target.ID) {
		bad(w, "turn your own TOTP off from your account (it takes a code)")
		return
	}
	if in.Role != nil && !s.mayGrantRole(p, *in.Role) {
		bad(w, "give a role whose scopes the caller itself holds (owner, admin, member or guest)")
		return
	}
	if err := s.db.updateUser(r.Context(), id, set); err != nil {
		failed(w)
		return
	}
	if in.Role != nil {
		switch err := s.SetRole(r.Context(), id, *in.Role); {
		case errors.Is(err, ErrLastOwner):
			apierr.Write(w, http.StatusConflict, "last_owner", "the last owner cannot be given another role", nil)
			return
		case err != nil:
			failed(w)
			return
		}
	}
	if in.ResetTOTP {
		if err := s.db.resetTOTP(r.Context(), id); err != nil {
			failed(w)
			return
		}
	}
	if in.Disabled != nil && *in.Disabled || in.Password != nil {
		_ = s.db.revokeSessionsExcept(r.Context(), id, "", time.Now())
	}
	u, _ := s.db.userByID(r.Context(), id)
	u.Role, _ = s.db.roleOf(r.Context(), s.db.st, id)
	writeJSON(w, http.StatusOK, u)
}

// mayGrantRole: a valid role whose scopes the caller holds (nobody grants more than they have).
func (s *Service) mayGrantRole(p libauth.Principal, role string) bool {
	if !ValidRole(role) {
		return false
	}
	_, ok := grantable(p.Scopes, RoleScopes(s.app.Name, role))
	return ok
}

// ---- API keys and pairing (machines) ----

func (s *Service) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.ListKeys(r.Context())
	if err != nil {
		failed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

type keyRequest struct {
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	Audiences []string `json:"audiences"`
	TTLHours  int      `json:"ttl_hours"`
}

func (s *Service) createKey(w http.ResponseWriter, r *http.Request) {
	var in keyRequest
	if !decode(w, r, &in) {
		return
	}
	p, _ := caller(r)
	name := strings.TrimSpace(in.Name)
	scopes, ok := grantable(p.Scopes, in.Scopes)
	if name == "" || len(name) > 120 || len(in.Scopes) == 0 || !ok || in.TTLHours < 0 {
		bad(w, "give a name, and scopes the caller itself holds")
		return
	}
	now := time.Now().UTC()
	c := Credential{ID: newID("k_"), Name: name, Scopes: scopes, Audiences: cleanList(in.Audiences), CreatedBy: p.ID, CreatedAt: now}
	if in.TTLHours > 0 {
		c.ExpiresAt = now.Add(time.Duration(in.TTLHours) * time.Hour)
	}
	secret := randomString(32)
	if err := s.db.createCredential(r.Context(), c, secret); err != nil {
		failed(w)
		return
	}
	s.emitKey(KeyCreated, c, p.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"key": keyPrefix + secret, "credential": c})
}

func cleanList(in []string) []string {
	var out []string
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && !strings.ContainsAny(v, " \t\n") {
			out = append(out, v)
		}
	}
	return out
}

func (s *Service) revokeKey(w http.ResponseWriter, r *http.Request) {
	switch err := s.RevokeKey(r.Context(), strings.TrimPrefix(r.URL.Path, "/api/identity/keys/")); {
	case errors.Is(err, ErrNotFound):
		notFound(w)
	case err != nil:
		failed(w)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// createPairingCode makes a one-time code another app redeems for an API key.
func (s *Service) createPairingCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		keyRequest
		TTLMinutes int `json:"ttl_minutes"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, _ := caller(r)
	scopes, ok := grantable(p.Scopes, in.Scopes)
	name := strings.TrimSpace(in.Name)
	if name == "" || len(in.Scopes) == 0 || !ok || in.TTLHours < 0 || in.TTLMinutes < 0 || in.TTLMinutes > 1440 {
		bad(w, "give a name, and scopes the caller itself holds; the code lives at most 24 hours")
		return
	}
	ttl := time.Duration(in.TTLMinutes) * time.Minute
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	now := time.Now().UTC()
	pc := pairingCode{ID: newID("pc_"), Name: name, Scopes: scopes, Audiences: cleanList(in.Audiences),
		KeyTTL: time.Duration(in.TTLHours) * time.Hour, CreatedBy: p.ID, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	secret := randomString(24)
	if err := s.db.createPairingCode(r.Context(), pc, secret); err != nil {
		failed(w)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": "pair_" + secret, "expires_at": pc.ExpiresAt})
}

// pair is the other app's side: a code in, an API key out. It is public and
// rate limited by address, since the caller has no identity yet.
func (s *Service) pair(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	key := "pair|" + clientAddr(r)
	now := time.Now().UTC()
	if wait := s.limiter.wait(key, now); wait > 0 {
		tooMany(w, wait)
		return
	}
	secretCode, ok := strings.CutPrefix(strings.TrimSpace(in.Code), "pair_")
	if !ok {
		s.limiter.fail(key, now)
		apierr.Write(w, http.StatusUnauthorized, "invalid_code", "the pairing code is not valid", nil)
		return
	}
	cred, secret, err := s.db.redeemPairingCode(r.Context(), secretCode, now, func(p pairingCode) (Credential, string) {
		c := Credential{ID: newID("k_"), Name: p.Name, Scopes: p.Scopes, Audiences: p.Audiences, CreatedBy: p.CreatedBy, CreatedAt: now}
		if p.KeyTTL > 0 {
			c.ExpiresAt = now.Add(p.KeyTTL)
		}
		return c, randomString(32)
	})
	if err != nil {
		s.limiter.fail(key, now)
		apierr.Write(w, http.StatusUnauthorized, "invalid_code", "the pairing code is not valid", nil)
		return
	}
	s.emitKey(KeyCreated, cred, cred.CreatedBy) // redeemed by an app with no principal; the code's maker
	writeJSON(w, http.StatusCreated, map[string]any{"key": keyPrefix + secret, "credential": cred, "token_url": "/api/token", "jwks_url": "/api/identity/jwks"})
}

// ---- tokens ----

func (s *Service) jwksHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(s.publishedJWKS())
}

// tokenHandler trades the caller's credential (a session, an API key or a token)
// for a short-lived JWT. The body may narrow the scopes, name another audience
// the credential allows, and ask for a shorter life. The reply has the shape of
// Tether's POST /auth/token, so a client points auth.mint_url here.
func (s *Service) tokenHandler(w http.ResponseWriter, r *http.Request) {
	p, ok := caller(r)
	if !ok {
		apierr.Write(w, http.StatusUnauthorized, "unauthenticated", "authentication is required", nil)
		return
	}
	var in struct {
		Scopes     []string `json:"scopes"`
		Audience   string   `json:"audience"`
		TTLSeconds int      `json:"ttl_seconds"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	scopes, ok := grantable(p.Scopes, in.Scopes)
	if !ok {
		apierr.Write(w, http.StatusForbidden, "scope_not_held", "a token cannot carry scopes the caller does not hold", nil)
		return
	}
	aud := in.Audience
	if aud == "" {
		aud = s.cfg.audience
	}
	if aud != s.cfg.audience && !s.mayMintFor(r.Context(), p, aud) {
		apierr.Write(w, http.StatusForbidden, "audience_not_allowed", "this credential may not mint for that audience", nil)
		return
	}
	ttl := s.cfg.tokenTTL
	if in.TTLSeconds > 0 {
		ttl = min(time.Duration(in.TTLSeconds)*time.Second, maxTokenTTL)
	}
	now := time.Now()
	c := s.newClaims(p.ID, p.Kind, aud, scopes, ttl, now)
	c.SID, c.Par = p.SessionID, p.TokenID
	tok, err := s.mint(c)
	if err != nil {
		failed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "token_type": "Bearer", "expires_at": time.Unix(c.Exp, 0).UTC(), "ttl_seconds": int(ttl / time.Second), "scopes": scopes, "audience": aud})
}

// mayMintFor says whether p may get a token for another audience: a credential
// lists the audiences it allows; a principal holding "*" or the app's admin
// scope may mint for any.
func (s *Service) mayMintFor(ctx context.Context, p libauth.Principal, aud string) bool {
	if covers(p.Scopes, "*") || s.isAdmin(p) {
		return true
	}
	if !strings.HasPrefix(p.ID, "key:") {
		return false
	}
	c, _, err := scanCredential(s.db.st.QueryRowContext(ctx, s.db.q(`SELECT `+credCols+` FROM identity_credentials WHERE id = ?`), strings.TrimPrefix(p.ID, "key:")))
	if err != nil {
		return false
	}
	for _, a := range c.Audiences {
		if a == aud {
			return true
		}
	}
	return false
}
