package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	libauth "github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/substrate/genui"
)

const ShowPatternToolName = "genui_show_pattern"

// ShowPatternTool is a raw-argument adapter for a host's agent tool registry.
// Mount it with the explicit show scope and write annotation (publishes a
// surface). A transport must pass original JSON bytes, not a float64-decoded
// map: the latter cannot preserve duplicate-key or numeric lexical evidence.
// It exposes no action execution, arbitrary component, URL or launch parameter.
type ShowPatternTool struct {
	service  *Service
	resolver ScopeResolver
	scope    string
}

func NewShowPatternTool(s *Service, resolver ScopeResolver, scope string) (*ShowPatternTool, error) {
	if s == nil || resolver == nil || scope == "" {
		return nil, ErrUnavailable
	}
	return &ShowPatternTool{s, resolver, scope}, nil
}
func (*ShowPatternTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"enum":["approval","form","list_detail"]},"params":{"type":"object"}},"required":["pattern","params"],"additionalProperties":false}`)
}
func (t *ShowPatternTool) Handle(ctx context.Context, raw json.RawMessage) (genui.Envelope, error) {
	p, ok := libauth.FromContext(ctx)
	if !ok || !p.Verified || p.ID == "" || !p.HasScope(t.scope) || (!p.ExpiresAt.IsZero() && !time.Now().Before(p.ExpiresAt)) {
		return genui.Envelope{}, ErrForbidden
	}
	if !p.ExpiresAt.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, p.ExpiresAt)
		defer cancel()
	}
	scope, err := t.resolver.Resolve(ctx, p)
	if err != nil || !validScope(scope) {
		return genui.Envelope{}, ErrForbidden
	}
	if len(raw) > genui.MaxActionBytes {
		return genui.Envelope{}, ErrInvalid
	}
	canonical, err := genui.CanonicalJSON(raw)
	if err != nil {
		return genui.Envelope{}, ErrInvalid
	}
	var q ShowRequest
	d := json.NewDecoder(bytes.NewReader(canonical))
	d.DisallowUnknownFields()
	if d.Decode(&q) != nil {
		return genui.Envelope{}, ErrInvalid
	}
	return t.service.Show(ctx, scope, q)
}
