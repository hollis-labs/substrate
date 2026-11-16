// Package genui defines a bounded, reviewed-pattern profile of A2UI inside the
// existing envelope protocol. Hosts own policy, identity, persistence and effects.
package genui

import "encoding/json"

const (
	ContractVersion       = "1"
	ProtocolVersion       = "1.0"
	TypeVersion           = "1.0.0"
	EnvelopeType          = "genui.declarative-surface"
	CatalogID             = "urn:hollis-labs:genui:catalog:1"
	FunctionName          = "genui_action"
	MaxSurfaceBytes       = 256 << 10
	MaxActionBytes        = 64 << 10
	MaxDepth              = 32
	MaxStringBytes        = 16384
	MaxCollection         = 256
	MaxSafeInteger  int64 = 9007199254740991
)

type Risk string

const (
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

type Subject struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type Basis struct {
	Revision           string `json:"revision"`
	ProjectionRevision string `json:"projectionRevision,omitempty"`
}
type Action struct {
	ID           string  `json:"id"`
	Subject      Subject `json:"subject"`
	Risk         Risk    `json:"risk,omitempty"`
	Basis        Basis   `json:"basis"`
	Confirmation string  `json:"confirmation,omitempty"`
}
type ActionArgs struct {
	Action Action         `json:"genui_action"`
	Input  map[string]any `json:"input"`
}
type FunctionCall struct {
	Name      string     `json:"@call"`
	CatalogID string     `json:"catalogId,omitempty"`
	Args      ActionArgs `json:"args"`
}
type ActionRequest struct {
	SurfaceID      string       `json:"surfaceId"`
	FunctionCallID string       `json:"functionCallId"`
	CallFunction   FunctionCall `json:"callFunction"`
}
type Call struct {
	Version           string        `json:"version"`
	CallAgentFunction ActionRequest `json:"callAgentFunction"`
}

type Status string

const (
	StatusOK                Status = "ok"
	StatusAccepted          Status = "accepted"
	StatusNeedsConfirmation Status = "needs_confirmation"
	StatusRefused           Status = "refused"
	StatusConflict          Status = "conflict"
	StatusStale             Status = "stale"
	StatusCanceled          Status = "canceled"
	StatusExpired           Status = "expired"
	StatusSuperseded        Status = "superseded"
	StatusFailed            Status = "failed"
)

func (s Status) Known() bool {
	switch s {
	case StatusOK, StatusAccepted, StatusNeedsConfirmation, StatusRefused, StatusConflict, StatusStale, StatusCanceled, StatusExpired, StatusSuperseded, StatusFailed:
		return true
	}
	return false
}
func (s Status) Terminal() bool {
	switch s {
	case StatusOK, StatusRefused, StatusConflict, StatusCanceled, StatusExpired, StatusSuperseded, StatusFailed:
		return true
	}
	return false
}

type Confirmation struct {
	What  string   `json:"what"`
	Scope []string `json:"scope"`
	TTLMS int64    `json:"ttlMs"`
	Who   string   `json:"who,omitempty"`
}
type Outcome struct {
	Status   Status          `json:"status"`
	Result   json.RawMessage `json:"result,omitempty"`
	Receipt  string          `json:"receipt,omitempty"`
	Handle   string          `json:"handle,omitempty"`
	Poll     *bool           `json:"poll,omitempty"`
	Confirm  *Confirmation   `json:"confirm,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Recorded *Outcome        `json:"recorded,omitempty"`
	Current  *Basis          `json:"current,omitempty"`
	Cause    string          `json:"cause,omitempty"`
	By       string          `json:"by,omitempty"`
	Code     string          `json:"code,omitempty"`
	Message  string          `json:"message,omitempty"`
	Detail   json.RawMessage `json:"detail,omitempty"`
}
type ProtocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type FunctionResponse struct {
	FunctionCallID string         `json:"functionCallId"`
	Value          *Outcome       `json:"value,omitempty"`
	Error          *ProtocolError `json:"error,omitempty"`
}
type Response struct {
	Version               string           `json:"version"`
	AgentFunctionResponse FunctionResponse `json:"agentFunctionResponse"`
}

// ResponseObservation preserves unknown outcomes without classifying them as a
// success, a terminal failure, or authorization to retry an effect.
type ResponseObservation struct {
	Response Response
	Known    bool
	Terminal bool
	Unknown  json.RawMessage
}

type Pattern string

const (
	PatternApproval   Pattern = "approval"
	PatternForm       Pattern = "form"
	PatternListDetail Pattern = "list_detail"
)

type ViewState string

const (
	ViewReady    ViewState = "ready"
	ViewLoading  ViewState = "loading"
	ViewEmpty    ViewState = "empty"
	ViewError    ViewState = "error"
	ViewStale    ViewState = "stale"
	ViewConflict ViewState = "conflict"
	ViewExpired  ViewState = "expired"
)

type Choice struct {
	Label        string       `json:"label"`
	CallFunction FunctionCall `json:"callFunction"`
}
type Change struct {
	Label  string `json:"label"`
	Before string `json:"before"`
	After  string `json:"after"`
}
type ApprovalProps struct {
	Title   string    `json:"title"`
	Summary string    `json:"summary,omitempty"`
	Changes []Change  `json:"changes"`
	Choices []Choice  `json:"choices"`
	State   ViewState `json:"state"`
}
type FormField struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Type      string `json:"type"`
	Required  bool   `json:"required"`
	Help      string `json:"help,omitempty"`
	Min       *int64 `json:"min,omitempty"`
	Max       *int64 `json:"max,omitempty"`
	MaxLength *int   `json:"maxLength,omitempty"`
}
type FormProps struct {
	Title  string      `json:"title"`
	Fields []FormField `json:"fields"`
	Submit Choice      `json:"submit"`
	State  ViewState   `json:"state"`
}
type ListItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}
type Detail struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}
type ListDetailProps struct {
	Title      string     `json:"title"`
	Items      []ListItem `json:"items"`
	SelectedID string     `json:"selectedId,omitempty"`
	Detail     *Detail    `json:"detail,omitempty"`
	State      ViewState  `json:"state"`
}
type Component struct {
	ID        string          `json:"id"`
	Component string          `json:"component"`
	Props     json.RawMessage `json:"props"`
}
type DataModel struct {
	Draft map[string]any `json:"draft,omitempty"`
}
type PatternMetadata struct {
	ID      Pattern `json:"id"`
	Version string  `json:"version"`
}
type SurfaceVersion struct {
	ContractVersion string `json:"contract_version"`
}
type Extensions struct {
	Surface SurfaceVersion  `json:"genui_surface"`
	Pattern PatternMetadata `json:"genui_pattern"`
}
type Metadata struct {
	Extensions Extensions `json:"extensions"`
}
type Surface struct {
	SurfaceID  string      `json:"surfaceId"`
	CatalogID  string      `json:"catalogId"`
	Components []Component `json:"components"`
	DataModel  DataModel   `json:"dataModel"`
	Metadata   Metadata    `json:"metadata"`
}
type SurfaceMessage struct {
	Version       string  `json:"version"`
	CreateSurface Surface `json:"createSurface"`
}

// Envelope is the deliberately restricted GenUI shape of envelopes.Envelope.
// It keeps its native field names; actions use A2UI RPC rather than legacy responses.
type Envelope struct {
	V            int            `json:"v"`
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	TypeVersion  string         `json:"typeVersion"`
	Title        string         `json:"title,omitempty"`
	Context      string         `json:"context,omitempty"`
	Presentation string         `json:"presentation,omitempty"`
	Data         SurfaceMessage `json:"data"`
}
