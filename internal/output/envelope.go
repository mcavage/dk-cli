package output

import "encoding/json"

// Envelope is the one shape every command prints to stdout, ok:true or
// ok:false, never anything else. Field order in the struct is the field
// order an agent sees in the raw JSON, which is why OK and Command come
// first: they are what a parser checks before anything else.
type Envelope struct {
	OK       bool      `json:"ok"`
	Command  string    `json:"command"`
	Data     any       `json:"data,omitempty"`
	Warnings []Warning `json:"warnings"`
	Meta     *Meta     `json:"meta,omitempty"`
	Error    *Error    `json:"error,omitempty"`
}

// Warning is a machine-checkable note riding along with a successful (or
// partially successful) response. Code is the same enum as Error.Code so an
// agent skimming only `warnings` for RESULT_TRUNCATED or STALE_DATA does not
// need a second vocabulary.
type Warning struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

// Error is the failure half of the envelope. Fix is always a literal,
// copy-pasteable command or "" (never null, never prose), because an agent
// that has to interpret prose to find the next command is the failure mode
// this contract exists to remove. Details carries structured context, most
// importantly DigiKey's correlationId, which is what their support asks for.
type Error struct {
	Code      Code           `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Fix       string         `json:"fix"`
	Details   map[string]any `json:"details,omitempty"`
}

// NewError builds an Error. fix is a required parameter rather than a field
// set after construction: the only way to get an Error out of this
// constructor is to have already decided whether a runnable fix exists, so
// "I forgot to set fix" cannot compile silently into an empty struct field.
// Pass "" explicitly when there genuinely is no fix.
func NewError(code Code, message string, retryable bool, fix string) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable, Fix: fix}
}

// WithDetails attaches structured context (e.g. {"upstream": {"correlationId": "..."}})
// to an error and returns it, so a call site reads as one expression.
func (e *Error) WithDetails(details map[string]any) *Error {
	e.Details = details
	return e
}

// Success builds an ok:true envelope. Warnings starts as an empty (non-nil)
// slice so it marshals as [] rather than null even before any warning is
// added: an agent that does `for w in warnings` should never have to
// null-check first.
func Success(command string, data any) *Envelope {
	return &Envelope{OK: true, Command: command, Data: data, Warnings: []Warning{}}
}

// Failure builds an ok:false envelope. Data is intentionally left unset:
// omitempty on Envelope.Data means a failure response has no `data` key at
// all, so "data present" is itself a reliable signal of success without even
// reading `ok`.
func Failure(command string, err *Error) *Envelope {
	return &Envelope{OK: false, Command: command, Warnings: []Warning{}, Error: err}
}

// WithMeta attaches meta and returns the envelope, for chaining at the call site.
func (e *Envelope) WithMeta(m *Meta) *Envelope {
	e.Meta = m
	return e
}

// AddWarning appends a warning and returns the envelope, for chaining.
func (e *Envelope) AddWarning(w Warning) *Envelope {
	e.Warnings = append(e.Warnings, w)
	return e
}

// MarshalJSON guarantees warnings is [] rather than null even if an Envelope
// was built by struct literal instead of Success/Failure, e.g. Envelope{} in
// a test or a future call site that forgets the constructor. A nil slice is
// valid Go but "warnings": null is exactly the kind of shape drift this
// package exists to prevent (D5), so the guarantee lives at marshal time,
// not just at construction time.
func (e Envelope) MarshalJSON() ([]byte, error) {
	type alias Envelope // avoid recursing back into this method
	a := alias(e)
	if a.Warnings == nil {
		a.Warnings = []Warning{}
	}
	return json.Marshal(a)
}
