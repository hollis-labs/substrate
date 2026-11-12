package identity

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/hollis-labs/substrate/apierr"
)

// rotateSigningKey is POST /api/identity/signing-key/rotate. The route's scope
// ("{app}.admin", declared in ui.json) is enforced by the guard.
func (s *Service) rotateSigningKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Compromised bool `json:"compromised"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	res, err := s.RotateSigningKey(in.Compromised)
	switch {
	case errors.Is(err, ErrRotating):
		apierr.Write(w, http.StatusConflict, "rotation_in_progress", "a signing-key rotation is already in progress", nil)
	case err != nil:
		slog.Error("identity: signing-key rotation failed", "error", err)
		failed(w)
	default:
		p, _ := caller(r)
		slog.Warn("identity: signing key rotated", "by", p.ID, "kid", res.KID, "retired_kid", res.RetiredKID, "compromised", in.Compromised)
		writeJSON(w, http.StatusOK, res)
	}
}

func (s *Service) signingKeyState(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.SigningKey())
}
