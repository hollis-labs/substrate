package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP is HMAC-SHA1; every authenticator app expects it.
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/substrate/apierr"
	"rsc.io/qr"
)

// TOTP: authenticator-app codes (RFC 6238: HMAC-SHA1, 30 seconds, 6 digits) as a second step
// after the password. A person turns it on for their own account: begin returns a secret (and
// a QR code of its otpauth:// link) to add to the app; confirm proves the app has it with one
// code, turns TOTP on and returns ten recovery codes, once. From then on a password sign-in
// also needs a code (or an unused recovery code). A passkey sign-in needs none: a passkey is
// already the stronger factor. A code is accepted one step either side of now and only once.
//
//	GET    /api/identity/totp                 {enabled, pending, recovery_codes_left, required}
//	POST   /api/identity/totp/begin           {} -> {secret, uri, qr_svg}
//	POST   /api/identity/totp/confirm         {code} -> {recovery_codes}
//	POST   /api/identity/totp/recovery-codes  {code} -> {recovery_codes} (a new set)
//	DELETE /api/identity/totp                 {code}: turn it off (a code or a recovery code)
//	POST   /api/login                         {username, password, code?}: 401 totp_required
//	                                          when the account has TOTP and no code was sent
//
// The secret is stored as is (an authenticator secret cannot be hashed); the recovery codes
// only as hashes. An administrator can reset someone's TOTP (PUT /api/identity/users/{id}
// {"reset_totp": true}) when they have lost the app and their recovery codes.
//
// identity.require_second_factor lists roles (e.g. [owner, admin]) whose accounts must have
// one: TOTP, or a passkey when passkeys are on. Until such an account has one, its sessions
// carry no scopes, so they reach only the signed-in identity routes (enough to enroll), and
// the profile says second_factor_required. Such an account with a passkey but no TOTP signs
// in with the passkey, not the password alone.

const (
	totpPeriod        = 30
	totpDigits        = 6
	recoveryCodeCount = 10
	// MethodTOTP and MethodRecovery are sessions signed in with a password and a second step.
	MethodTOTP     = "password+totp"
	MethodRecovery = "password+recovery"
)

// ErrTOTPCode is a TOTP or recovery code that is wrong, expired or already used.
var ErrTOTPCode = errors.New("identity: the code is wrong or was already used")

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// totpAt is the code for a secret at a time step.
func totpAt(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // time steps are positive
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, n%1_000_000)
}

// totpMatch finds the step (now, or one either side) whose code is code, after last.
func totpMatch(secretB32, code string, now time.Time, last int64) (int64, bool) {
	secret, err := b32.DecodeString(secretB32)
	if err != nil {
		return 0, false
	}
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for _, step := range []int64{cur - 1, cur, cur + 1} {
		if step > last && subtle.ConstantTimeCompare([]byte(totpAt(secret, step)), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func newTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic("identity: no randomness: " + err.Error())
	}
	return b32.EncodeToString(b)
}

// otpauthURI is the link authenticator apps import (usually from its QR code).
func otpauthURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {fmt.Sprint(totpDigits)}, "period": {fmt.Sprint(totpPeriod)}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// qrSVG draws text as a QR code, an SVG with a four-module quiet zone.
func qrSVG(text string) (string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	n := c.Size + 8
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n, n, n)
	for y := range c.Size {
		for x := range c.Size {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}

// newRecoveryCodes makes a set: 16 base32 characters each (80 bits), shown in groups of four.
func newRecoveryCodes() []string {
	out := make([]string, recoveryCodeCount)
	for i := range out {
		b := make([]byte, 10)
		if _, err := rand.Read(b); err != nil {
			panic("identity: no randomness: " + err.Error())
		}
		s := strings.ToLower(b32.EncodeToString(b))
		out[i] = s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
	}
	return out
}

// normalRecoveryCode is a typed recovery code as it was hashed: lowercase, no dashes or spaces.
func normalRecoveryCode(code string) string {
	return strings.NewReplacer("-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(code)))
}

// ---- store ----

type totpState struct {
	secret   string
	enabled  bool
	lastStep int64
}

func (d db) totp(ctx context.Context, userID string) (totpState, bool) {
	var t totpState
	var enabled int64
	err := d.st.QueryRowContext(ctx, d.q(`SELECT secret, enabled_at, last_step FROM identity_totp WHERE user_id = ?`), userID).Scan(&t.secret, &enabled, &t.lastStep)
	if err != nil {
		return totpState{}, false
	}
	t.enabled = enabled != 0
	return t, true
}

func (d db) totpEnabled(ctx context.Context, userID string) bool {
	t, ok := d.totp(ctx, userID)
	return ok && t.enabled
}

// useTOTPStep records a step as used; false when it (or a later one) already was.
func (d db) useTOTPStep(ctx context.Context, userID string, step int64) bool {
	res, err := d.st.ExecContext(ctx, d.q(`UPDATE identity_totp SET last_step = ? WHERE user_id = ? AND last_step < ?`), step, userID, step)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func (d db) recoveryCodesLeft(ctx context.Context, userID string) int {
	var n int
	_ = d.st.QueryRowContext(ctx, d.q(`SELECT COUNT(*) FROM identity_recovery_codes WHERE user_id = ? AND used_at = 0`), userID).Scan(&n)
	return n
}

func (d db) useRecoveryCode(ctx context.Context, userID, code string, now time.Time) bool {
	c := normalRecoveryCode(code)
	if len(c) != 16 {
		return false
	}
	res, err := d.st.ExecContext(ctx, d.q(`UPDATE identity_recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at = 0`), now.Unix(), userID, hashToken(c))
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func (d db) replaceRecoveryCodes(ctx context.Context, tx store.Tx, userID string, codes []string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, d.q(`DELETE FROM identity_recovery_codes WHERE user_id = ?`), userID); err != nil {
		return err
	}
	for _, c := range codes {
		if _, err := tx.ExecContext(ctx, d.q(`INSERT INTO identity_recovery_codes (id, user_id, code_hash, created_at) VALUES (?,?,?,?)`),
			newID("rc_"), userID, hashToken(normalRecoveryCode(c)), now.Unix()); err != nil {
			return err
		}
	}
	return nil
}

// resetTOTP removes a user's TOTP and recovery codes.
func (d db) resetTOTP(ctx context.Context, userID string) error {
	return d.st.WriteTx(ctx, func(ctx context.Context, tx store.Tx) error {
		if _, err := tx.ExecContext(ctx, d.q(`DELETE FROM identity_totp WHERE user_id = ?`), userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, d.q(`DELETE FROM identity_recovery_codes WHERE user_id = ?`), userID)
		return err
	})
}

// checkSecondStep accepts a TOTP code or, failing that, an unused recovery code, and spends it.
// It answers how the person signed in.
func (s *Service) checkSecondStep(ctx context.Context, userID, code string) (string, error) {
	t, ok := s.db.totp(ctx, userID)
	if !ok || !t.enabled {
		return "", ErrTOTPCode
	}
	if step, ok := totpMatch(t.secret, code, s.now(), t.lastStep); ok {
		if s.db.useTOTPStep(ctx, userID, step) {
			return MethodTOTP, nil
		}
		return "", ErrTOTPCode
	}
	if s.db.useRecoveryCode(ctx, userID, code, s.now()) {
		return MethodRecovery, nil
	}
	return "", ErrTOTPCode
}

// ---- the second-factor requirement ----

// secondFactorRequired says the role must have a second factor and the account has none yet.
func (s *Service) secondFactorRequired(ctx context.Context, userID, role string) bool {
	if !slices.Contains(s.cfg.requireSecondFactor, role) {
		return false
	}
	if s.db.totpEnabled(ctx, userID) {
		return false
	}
	return !s.cfg.passkeys || s.db.passkeyCount(ctx, userID) == 0
}

// passkeyInsteadOfPassword: an account whose role needs a second factor, with a passkey and no
// TOTP, signs in with the passkey.
func (s *Service) passkeyInsteadOfPassword(ctx context.Context, userID string) bool {
	if len(s.cfg.requireSecondFactor) == 0 || !s.cfg.passkeys || s.db.totpEnabled(ctx, userID) {
		return false
	}
	role, _ := s.db.roleOf(ctx, s.db.st, userID)
	return slices.Contains(s.cfg.requireSecondFactor, role) && s.db.passkeyCount(ctx, userID) > 0
}

// ---- handlers ----

func (s *Service) totpRoutes(h func(http.HandlerFunc) http.Handler) map[string]http.Handler {
	return map[string]http.Handler{
		"GET /api/identity/totp":                 h(s.totpStatus),
		"POST /api/identity/totp/begin":          h(s.totpBegin),
		"POST /api/identity/totp/confirm":        h(s.totpConfirm),
		"POST /api/identity/totp/recovery-codes": h(s.totpRecoveryCodes),
		"DELETE /api/identity/totp":              h(s.totpDisable),
	}
}

func (s *Service) totpStatus(w http.ResponseWriter, r *http.Request) {
	u, ok := s.passkeyCaller(w, r)
	if !ok {
		return
	}
	t, has := s.db.totp(r.Context(), u.ID)
	role, _ := s.db.roleOf(r.Context(), s.db.st, u.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": has && t.enabled, "pending": has && !t.enabled,
		"recovery_codes_left": s.db.recoveryCodesLeft(r.Context(), u.ID),
		"required":            slices.Contains(s.cfg.requireSecondFactor, role),
	})
}

func (s *Service) totpBegin(w http.ResponseWriter, r *http.Request) {
	u, ok := s.passkeyCaller(w, r)
	if !ok {
		return
	}
	if s.db.totpEnabled(r.Context(), u.ID) {
		apierr.Write(w, http.StatusConflict, "totp_enabled", "TOTP is already on; turn it off first to move it to another app", nil)
		return
	}
	secret := newTOTPSecret()
	now := s.now().Unix()
	err := s.db.st.WriteTx(r.Context(), func(ctx context.Context, tx store.Tx) error {
		if _, err := tx.ExecContext(ctx, s.db.q(`DELETE FROM identity_totp WHERE user_id = ? AND enabled_at = 0`), u.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, s.db.q(`INSERT INTO identity_totp (user_id, secret, created_at) VALUES (?,?,?)`), u.ID, secret, now)
		return err
	})
	if err != nil {
		failed(w)
		return
	}
	uri := otpauthURI(s.app.Name, u.Username, secret)
	svg, err := qrSVG(uri)
	if err != nil {
		failed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "uri": uri, "qr_svg": svg})
}

// codeAttempt rate-limits a signed-in person's code entries; false has answered.
func (s *Service) codeAttempt(w http.ResponseWriter, userID string) (string, bool) {
	key := "totp|" + userID
	if wait := s.limiter.wait(key, s.now()); wait > 0 {
		tooMany(w, wait)
		return key, false
	}
	return key, true
}

func badCode(w http.ResponseWriter) {
	apierr.Write(w, http.StatusUnauthorized, "invalid_code", "the code is wrong or was already used", nil)
}

func (s *Service) totpConfirm(w http.ResponseWriter, r *http.Request) {
	u, ok := s.passkeyCaller(w, r)
	if !ok {
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	key, ok := s.codeAttempt(w, u.ID)
	if !ok {
		return
	}
	t, has := s.db.totp(r.Context(), u.ID)
	if !has || t.enabled {
		apierr.Write(w, http.StatusConflict, "no_enrollment", "start again: there is no TOTP setup waiting for a code", nil)
		return
	}
	now := s.now()
	step, match := totpMatch(t.secret, in.Code, now, 0)
	if !match {
		s.limiter.fail(key, now)
		badCode(w)
		return
	}
	codes := newRecoveryCodes()
	err := s.db.st.WriteTx(r.Context(), func(ctx context.Context, tx store.Tx) error {
		res, err := tx.ExecContext(ctx, s.db.q(`UPDATE identity_totp SET enabled_at = ?, last_step = ? WHERE user_id = ? AND enabled_at = 0`), now.Unix(), step, u.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return sql.ErrNoRows
		}
		return s.db.replaceRecoveryCodes(ctx, tx, u.ID, codes, now)
	})
	if err != nil {
		failed(w)
		return
	}
	s.limiter.clear(key)
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// spendCode takes a code (TOTP or recovery) from a signed-in person; false has answered.
func (s *Service) spendCode(w http.ResponseWriter, r *http.Request, userID string) bool {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return false
	}
	key, ok := s.codeAttempt(w, userID)
	if !ok {
		return false
	}
	if !s.db.totpEnabled(r.Context(), userID) {
		apierr.Write(w, http.StatusConflict, "totp_off", "TOTP is not on for this account", nil)
		return false
	}
	if _, err := s.checkSecondStep(r.Context(), userID, in.Code); err != nil {
		s.limiter.fail(key, s.now())
		badCode(w)
		return false
	}
	s.limiter.clear(key)
	return true
}

func (s *Service) totpRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	u, ok := s.passkeyCaller(w, r)
	if !ok || !s.spendCode(w, r, u.ID) {
		return
	}
	codes := newRecoveryCodes()
	err := s.db.st.WriteTx(r.Context(), func(ctx context.Context, tx store.Tx) error {
		return s.db.replaceRecoveryCodes(ctx, tx, u.ID, codes, s.now())
	})
	if err != nil {
		failed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (s *Service) totpDisable(w http.ResponseWriter, r *http.Request) {
	u, ok := s.passkeyCaller(w, r)
	if !ok {
		return
	}
	role, _ := s.db.roleOf(r.Context(), s.db.st, u.ID)
	if slices.Contains(s.cfg.requireSecondFactor, role) && (!s.cfg.passkeys || s.db.passkeyCount(r.Context(), u.ID) == 0) {
		apierr.Write(w, http.StatusConflict, "second_factor_required", "your role needs a second factor: add a passkey before turning TOTP off", nil)
		return
	}
	if !s.spendCode(w, r, u.ID) {
		return
	}
	if err := s.db.resetTOTP(r.Context(), u.ID); err != nil {
		failed(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
