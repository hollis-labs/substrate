package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"
)

// The signing key ring. One active Ed25519 key signs; retired keys stay in the
// published set, verify-only, until their not_after, so a rotation never
// invalidates a token before its own exp (docs/identity.md, "Rotating the
// signing key"). Disk layout beside key_file:
//
//	<key_file>               active private key (PEM, 0600)
//	<key_file>.retired.json  [{kid, x, retired_at, not_after}], public keys only
//	<key_file>.jwks.json     derived: active + retired within not_after

// minRetiredTTL is the longest token life plus ten minutes of skew: after it no
// unexpired token can have been signed by the retired key. It is both the
// default and the floor of retired_key_ttl.
const minRetiredTTL = maxTokenTTL + 10*time.Minute

type retiredKey struct {
	KID       string    `json:"kid"`
	X         string    `json:"x"`
	RetiredAt time.Time `json:"retired_at"`
	NotAfter  time.Time `json:"not_after"`
}

// keyState is an immutable snapshot; the Service swaps whole snapshots.
type keyState struct {
	kid     string
	priv    ed25519.PrivateKey
	created time.Time
	retired []retiredKey
	jwt     libauth.Verifier
}

// nextExpiry is the earliest not_after among the retired keys, or zero.
func (k *keyState) nextExpiry() time.Time {
	var first time.Time
	for _, r := range k.retired {
		if first.IsZero() || r.NotAfter.Before(first) {
			first = r.NotAfter
		}
	}
	return first
}

func kidOf(pub ed25519.PublicKey) string { return base64.RawURLEncoding.EncodeToString(pub[:8]) }

func jwk(kid string, x []byte) map[string]string {
	return map[string]string{"kty": "OKP", "crv": "Ed25519", "use": "sig", "alg": "EdDSA", "kid": kid,
		"x": base64.RawURLEncoding.EncodeToString(x)}
}

// jwksDoc is the published set: the active key, then retired keys still within
// not_after at now.
func jwksDoc(kid string, pub ed25519.PublicKey, retired []retiredKey, now time.Time) []byte {
	keys := []map[string]string{jwk(kid, pub)}
	for _, r := range retired {
		if now.Before(r.NotAfter) {
			x, _ := base64.RawURLEncoding.DecodeString(r.X)
			keys = append(keys, jwk(r.KID, x))
		}
	}
	doc, _ := json.Marshal(map[string]any{"keys": keys})
	return doc
}

func liveKeys(in []retiredKey, now time.Time) []retiredKey {
	out := make([]retiredKey, 0, len(in))
	for _, r := range in {
		if now.Before(r.NotAfter) {
			out = append(out, r)
		}
	}
	return out
}

// ---- files ----

func (s *Service) retiredPath() string { return s.cfg.keyFile + ".retired.json" }
func (s *Service) jwksPath() string    { return s.cfg.keyFile + ".jwks.json" }

// writeAtomic writes data to a temp file beside path, syncs it and renames it
// over path.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := writeSynced(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func writeSynced(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) // #nosec G304 -- operator-configured path
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (s *Service) writeRetired(list []retiredKey) error {
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.retiredPath(), b, 0o600)
}

// readRetired loads the retired set. A missing file is an empty set; a file
// that cannot be read or fails validation is an error, never a silent drop: the
// keys in it may still verify live tokens.
func (s *Service) readRetired() ([]retiredKey, error) {
	b, err := os.ReadFile(s.retiredPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: %s: %w (delete it to accept that retired keys stop verifying)", s.retiredPath(), err)
	}
	var list []retiredKey
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("identity: %s is corrupt: %w (delete it to accept that retired keys stop verifying)", s.retiredPath(), err)
	}
	for i, r := range list {
		x, err := base64.RawURLEncoding.DecodeString(r.X)
		if err != nil || len(x) != ed25519.PublicKeySize || r.KID == "" || r.KID != kidOf(x) || r.NotAfter.IsZero() {
			return nil, fmt.Errorf("identity: %s entry %d is corrupt (delete the file to accept that retired keys stop verifying)", s.retiredPath(), i)
		}
	}
	return list, nil
}

// stageJWKS writes doc beside the final JWKS file and builds a verifier from it
// (the libs verifier reads its keys from a file). The caller renames the staged
// file into place with commitJWKS.
func (s *Service) stageJWKS(doc []byte) (libauth.Verifier, error) {
	tmp := s.jwksPath() + ".tmp"
	if err := writeSynced(tmp, doc, 0o644); err != nil { // #nosec G302 -- a public key set
		return nil, err
	}
	jv, err := libauth.NewJWTVerifier(libauth.JWTConfig{Issuer: s.cfg.issuer, Audience: s.cfg.audience, BootstrapKeyFile: tmp})
	if err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("identity: token verifier: %w", err)
	}
	return jv, nil
}

func (s *Service) commitJWKS() error { return os.Rename(s.jwksPath()+".tmp", s.jwksPath()) }

// ---- start ----

// loadKey reads the signing key (creating it when absent), recovers the retired
// set and publishes the key set.
func (s *Service) loadKey() error {
	_ = os.Remove(s.cfg.keyFile + ".new") // an interrupted rotation never reached its commit point
	var priv ed25519.PrivateKey
	b, err := os.ReadFile(s.cfg.keyFile)
	switch {
	case err == nil:
		blk, _ := pem.Decode(b)
		if blk == nil {
			return fmt.Errorf("identity: %s is not a PEM key", s.cfg.keyFile)
		}
		k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
		if err != nil {
			return fmt.Errorf("identity: %s: %w", s.cfg.keyFile, err)
		}
		ek, ok := k.(ed25519.PrivateKey)
		if !ok {
			return fmt.Errorf("identity: %s is not an Ed25519 key", s.cfg.keyFile)
		}
		priv = ek
	case errors.Is(err, os.ErrNotExist):
		if err := s.refuseSecondIssuer(); err != nil {
			return err
		}
		_, priv, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if err := os.WriteFile(s.cfg.keyFile, encodeKey(priv), 0o600); err != nil {
			return fmt.Errorf("identity: cannot write the signing key: %w", err)
		}
	default:
		return fmt.Errorf("identity: signing key: %w", err)
	}
	created := time.Now()
	if fi, err := os.Stat(s.cfg.keyFile); err == nil {
		created = fi.ModTime()
	}
	pub := priv.Public().(ed25519.PublicKey)
	kid := kidOf(pub)

	list, err := s.readRetired()
	if err != nil {
		return err
	}
	now := s.now()
	kept := make([]retiredKey, 0, len(list))
	for _, r := range liveKeys(list, now) {
		if r.KID != kid { // a crash before the commit point leaves the still-active key listed
			kept = append(kept, r)
		}
	}
	if len(kept) != len(list) {
		if err := s.writeRetired(kept); err != nil {
			return fmt.Errorf("identity: %w", err)
		}
	}
	jv, err := s.stageJWKS(jwksDoc(kid, pub, kept, now))
	if err != nil {
		return err
	}
	if err := s.commitJWKS(); err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	s.keys.Store(&keyState{kid: kid, priv: priv, created: created, retired: kept, jwt: jv})
	return nil
}

// refuseSecondIssuer stops a process from minting a signing key when the database
// already holds identity rows (users or API keys) but no key file exists where
// this process's config points. Those rows belong to an instance that already has
// a signing key, somewhere else; a second key here would make two issuers, each
// verifying only its own tokens. A database with no identity rows is a fresh
// instance and creates its key. allow_new_signing_key is the deliberate override
// (a lost key, or a deliberate move).
func (s *Service) refuseSecondIssuer() error {
	if s.cfg.allowNewKey {
		return nil
	}
	ctx := context.Background()
	users, err := s.db.userCount(ctx)
	if err != nil {
		return fmt.Errorf("identity: checking for existing identity rows: %w", err)
	}
	keys, err := s.db.credentialCount(ctx)
	if err != nil {
		return fmt.Errorf("identity: checking for existing identity rows: %w", err)
	}
	if users == 0 && keys == 0 {
		return nil
	}
	return fmt.Errorf("identity: the database already holds identity rows (%d users, %d API keys) but there is no signing key at key_file %q, so creating one here would make a second issuer. "+
		"Point identity.key_file (or SUBSTRATE_CONFIG) at the key the service uses, or the database at the right file; "+
		"only if the key is truly lost or this is a deliberate new issuer, set identity.allow_new_signing_key: true for one start", users, keys, s.cfg.keyFile)
}

func encodeKey(priv ed25519.PrivateKey) []byte {
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// ---- reading the ring ----

// current returns the key snapshot, first dropping retired keys past not_after.
func (s *Service) current() *keyState {
	st := s.keys.Load()
	if ne := st.nextExpiry(); !ne.IsZero() && !s.now().Before(ne) {
		if err := s.prune(); err != nil {
			slog.Warn("identity: pruning retired signing keys", "error", err)
		}
		st = s.keys.Load()
	}
	return st
}

// prune removes retired keys past not_after from the set, the verifier and the
// files. The in-memory set is replaced before the files are written: expiry is
// not held up by a disk error.
func (s *Service) prune() error {
	s.rot.Lock()
	defer s.rot.Unlock()
	st := s.keys.Load()
	now := s.now()
	live := liveKeys(st.retired, now)
	if len(live) == len(st.retired) {
		return nil
	}
	pub := st.priv.Public().(ed25519.PublicKey)
	jv, err := s.stageJWKS(jwksDoc(st.kid, pub, live, now))
	if err != nil {
		return err
	}
	next := *st
	next.retired, next.jwt = live, jv
	s.keys.Store(&next)
	if err := s.writeRetired(live); err != nil {
		return err
	}
	return s.commitJWKS()
}

// publishedJWKS is the key set as served at /api/identity/jwks.
func (s *Service) publishedJWKS() []byte {
	st := s.current()
	return jwksDoc(st.kid, st.priv.Public().(ed25519.PublicKey), st.retired, s.now())
}

// ---- rotation ----

// ErrRotating is returned when a rotation is already in progress.
var ErrRotating = errors.New("identity: a signing-key rotation is already in progress")

// Rotation is the outcome of RotateSigningKey.
type Rotation struct {
	KID        string    `json:"kid"`
	RetiredKID string    `json:"retired_kid"`
	NotAfter   time.Time `json:"not_after"`
}

// RotateSigningKey replaces the active signing key. The old key stays in the
// published set, verify-only, until retired_key_ttl has passed (so every token
// it signed outlives its own exp first); with compromised it leaves the set at
// once. Order on disk: the retired record, then an atomic rename of the new key
// over key_file (the commit point), then the JWKS file; only then does the
// running process swap. An error before the commit point changes nothing in
// memory. A second call while one runs returns ErrRotating.
func (s *Service) RotateSigningKey(compromised bool) (Rotation, error) {
	if !s.rot.TryLock() {
		return Rotation{}, ErrRotating
	}
	defer s.rot.Unlock()
	st := s.keys.Load()
	now := s.now().UTC()
	oldPub := st.priv.Public().(ed25519.PublicKey)

	newPub, newPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Rotation{}, err
	}
	newKID := kidOf(newPub)
	if newKID == st.kid {
		return Rotation{}, errors.New("identity: generated a key with the active key id; try again")
	}
	notAfter := now.Add(s.cfg.retiredTTL)
	if compromised {
		notAfter = now
	}
	retired := liveKeys(st.retired, now)
	if !compromised {
		retired = append(retired, retiredKey{KID: st.kid, X: base64.RawURLEncoding.EncodeToString(oldPub), RetiredAt: now, NotAfter: notAfter})
	}

	// Step 2: the retired record first, so the old key is never unlisted.
	if err := s.writeRetired(retired); err != nil {
		return Rotation{}, fmt.Errorf("identity: %w", err)
	}
	// Stage everything that can fail before the commit point.
	jv, err := s.stageJWKS(jwksDoc(newKID, newPub, retired, now))
	if err != nil {
		return Rotation{}, err
	}
	newFile := s.cfg.keyFile + ".new"
	if err := writeSynced(newFile, encodeKey(newPriv), 0o600); err != nil {
		return Rotation{}, fmt.Errorf("identity: %w", err)
	}
	if err := s.faultAt("before-commit"); err != nil {
		_ = os.Remove(newFile)
		return Rotation{}, err
	}
	// Step 3: the commit point.
	if err := os.Rename(newFile, s.cfg.keyFile); err != nil {
		_ = os.Remove(newFile)
		return Rotation{}, fmt.Errorf("identity: %w", err)
	}
	// Past the commit point the new key is the key. A failure from here is
	// logged, not returned: the process must follow the disk, and the JWKS file
	// is rebuilt at the next start.
	_ = s.faultAt("after-commit")
	if err := s.commitJWKS(); err != nil {
		slog.Error("identity: rotated, but the JWKS file could not be written", "error", err)
	}
	_ = s.faultAt("after-jwks")
	s.keys.Store(&keyState{kid: newKID, priv: newPriv, created: now, retired: retired, jwt: jv})
	return Rotation{KID: newKID, RetiredKID: st.kid, NotAfter: notAfter}, nil
}

// SigningKeyInfo is the state GET /api/identity/signing-key reports.
type SigningKeyInfo struct {
	Active  ActiveKey    `json:"active"`
	Retired []RetiredKey `json:"retired"`
}

// ActiveKey describes the key that signs.
type ActiveKey struct {
	KID       string    `json:"kid"`
	CreatedAt time.Time `json:"created_at"`
}

// RetiredKey describes a verify-only key and when it leaves the set.
type RetiredKey struct {
	KID       string    `json:"kid"`
	RetiredAt time.Time `json:"retired_at"`
	NotAfter  time.Time `json:"not_after"`
}

// SigningKey reports the active and retired signing keys. Public data only.
func (s *Service) SigningKey() SigningKeyInfo {
	st := s.current()
	info := SigningKeyInfo{Active: ActiveKey{KID: st.kid, CreatedAt: st.created.UTC()}, Retired: []RetiredKey{}}
	for _, r := range st.retired {
		info.Retired = append(info.Retired, RetiredKey{KID: r.KID, RetiredAt: r.RetiredAt, NotAfter: r.NotAfter})
	}
	return info
}

func (s *Service) faultAt(point string) error {
	if s.fault == nil {
		return nil
	}
	return s.fault(point)
}
