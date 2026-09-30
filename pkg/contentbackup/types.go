package contentbackup

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const (
	MetadataVersion      = 1
	MaxMetadataBytes     = 64 * 1024
	MaxBodyBytesPerSide  = 8 * 1024 * 1024
	EnvelopeBodyEncoding = "base64"
)

var (
	ErrInvalidMetadata     = errors.New("invalid content backup metadata")
	ErrMetadataTooLarge    = errors.New("content backup metadata exceeds the 64 KiB encoding limit")
	ErrBodyBytes           = errors.New("content backup body byte counters are invalid")
	ErrSessionInconsistent = errors.New("content backup session fields are inconsistent")
	ErrInvalidEnvelope     = errors.New("invalid content backup envelope")
	ErrEnvelopeBody        = errors.New("content backup envelope body does not match its metadata")
	ErrInvalidFrameSHA256  = errors.New("invalid content backup frame sha256")
	ErrInvalidSiteLabel    = errors.New("invalid content backup site label")
	ErrInvalidJobID        = errors.New("invalid content backup job id")
	ErrInvalidSessionKey   = errors.New("invalid content backup session key")
	ErrInvalidUserID       = errors.New("invalid content backup user id")
	ErrInvalidStartTime    = errors.New("invalid content backup request start time")
	ErrInvalidConfig       = errors.New("invalid content backup config")
	ErrRetentionTooShort   = errors.New("content backup index retention must not be shorter than content retention")
)

// ValidationError names the offending field so callers can surface a distinct reason instead of a
// generic rejection; Errs carries every sentinel the error matches.
type ValidationError struct {
	Field  string
	Reason string
	Errs   []error
}

func (e *ValidationError) Error() string {
	return "contentbackup: " + e.Field + ": " + e.Reason
}

func (e *ValidationError) Unwrap() []error {
	return e.Errs
}

type BodyMeta struct {
	ContentType   string `json:"content_type"`
	CapturedBytes int64  `json:"captured_bytes"`
	ObservedBytes int64  `json:"observed_bytes"`
	Truncated     bool   `json:"truncated"`
	Complete      bool   `json:"complete"`
}

// Metadata is the frame metadata: flat snake_case keys, one encoding fixed for the whole archive life.
type Metadata struct {
	Version              int       `json:"version"`
	SiteID               string    `json:"site_id"`
	StorageNodeID        string    `json:"storage_node_id"`
	JobID                string    `json:"job_id"`
	RequestID            string    `json:"request_id"`
	TargetID             string    `json:"target_id"`
	ConfigVersion        int64     `json:"config_version"`
	RequestStartedAt     time.Time `json:"request_started_at"`
	UserID               int       `json:"user_id"`
	TokenID              int       `json:"token_id"`
	ChannelID            int       `json:"channel_id"`
	ChannelName          string    `json:"channel_name"`
	Model                string    `json:"model"`
	Endpoint             string    `json:"endpoint"`
	TerminalReason       string    `json:"terminal_reason"`
	ChannelType          int       `json:"channel_type"`
	HTTPStatus           int       `json:"http_status"`
	UpstreamRequestID    *string   `json:"upstream_request_id"`
	SessionSource        *string   `json:"session_source"`
	SessionValue         *string   `json:"session_value"`
	SessionMissingReason *string   `json:"session_missing_reason"`
	Stream               bool      `json:"stream"`
	Request              BodyMeta  `json:"request"`
	Response             BodyMeta  `json:"response"`
}

// Capture transfers ownership of fixed chunks; Release runs exactly once per the TryEnqueue contract.
type Capture struct {
	Meta           Metadata
	RequestChunks  [][]byte
	ResponseChunks [][]byte
	Release        func()
}

type EnvelopeBody struct {
	ContentType   string `json:"content_type"`
	Encoding      string `json:"encoding"`
	Body          string `json:"body"`
	CapturedBytes int64  `json:"captured_bytes"`
	ObservedBytes int64  `json:"observed_bytes"`
	Truncated     bool   `json:"truncated"`
	Complete      bool   `json:"complete"`
}

type EnvelopeChannel struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Type int    `json:"type"`
}

type EnvelopeSession struct {
	Source        *string `json:"source"`
	Value         *string `json:"value"`
	MissingReason *string `json:"missing_reason"`
}

// Envelope is the self describing on disk form from section 5.2; bodies are base64 text.
type Envelope struct {
	Version           int             `json:"version"`
	JobID             string          `json:"job_id"`
	RequestID         string          `json:"request_id"`
	UpstreamRequestID *string         `json:"upstream_request_id"`
	SiteID            string          `json:"site_id"`
	StorageNodeID     string          `json:"storage_node_id"`
	TargetID          string          `json:"target_id"`
	ConfigVersion     int64           `json:"config_version"`
	FrameSHA256       string          `json:"frame_sha256"`
	RemotePath        string          `json:"remote_path"`
	RequestStartedAt  time.Time       `json:"request_started_at"`
	UserID            int             `json:"user_id"`
	TokenID           int             `json:"token_id"`
	Channel           EnvelopeChannel `json:"channel"`
	Model             string          `json:"model"`
	Session           EnvelopeSession `json:"session"`
	Endpoint          string          `json:"endpoint"`
	Stream            bool            `json:"stream"`
	HTTPStatus        int             `json:"http_status"`
	TerminalReason    string          `json:"terminal_reason"`
	Request           EnvelopeBody    `json:"request"`
	Response          EnvelopeBody    `json:"response"`
}

// envelopeHead keeps the streamed encoder byte identical to common.Marshal(Envelope): everything
// before the two body strings is marshalled as one object and only its closing brace is dropped.
type envelopeHead struct {
	Version           int             `json:"version"`
	JobID             string          `json:"job_id"`
	RequestID         string          `json:"request_id"`
	UpstreamRequestID *string         `json:"upstream_request_id"`
	SiteID            string          `json:"site_id"`
	StorageNodeID     string          `json:"storage_node_id"`
	TargetID          string          `json:"target_id"`
	ConfigVersion     int64           `json:"config_version"`
	FrameSHA256       string          `json:"frame_sha256"`
	RemotePath        string          `json:"remote_path"`
	RequestStartedAt  time.Time       `json:"request_started_at"`
	UserID            int             `json:"user_id"`
	TokenID           int             `json:"token_id"`
	Channel           EnvelopeChannel `json:"channel"`
	Model             string          `json:"model"`
	Session           EnvelopeSession `json:"session"`
	Endpoint          string          `json:"endpoint"`
	Stream            bool            `json:"stream"`
	HTTPStatus        int             `json:"http_status"`
	TerminalReason    string          `json:"terminal_reason"`
}

type envelopeBodyPrefix struct {
	ContentType string `json:"content_type"`
	Encoding    string `json:"encoding"`
	Body        string `json:"body"`
}

type envelopeBodySuffix struct {
	CapturedBytes int64 `json:"captured_bytes"`
	ObservedBytes int64 `json:"observed_bytes"`
	Truncated     bool  `json:"truncated"`
	Complete      bool  `json:"complete"`
}

func (e Envelope) Metadata() Metadata {
	return Metadata{
		Version:              e.Version,
		SiteID:               e.SiteID,
		StorageNodeID:        e.StorageNodeID,
		JobID:                e.JobID,
		RequestID:            e.RequestID,
		TargetID:             e.TargetID,
		ConfigVersion:        e.ConfigVersion,
		RequestStartedAt:     e.RequestStartedAt,
		UserID:               e.UserID,
		TokenID:              e.TokenID,
		ChannelID:            e.Channel.ID,
		ChannelName:          e.Channel.Name,
		Model:                e.Model,
		Endpoint:             e.Endpoint,
		TerminalReason:       e.TerminalReason,
		ChannelType:          e.Channel.Type,
		HTTPStatus:           e.HTTPStatus,
		UpstreamRequestID:    e.UpstreamRequestID,
		SessionSource:        e.Session.Source,
		SessionValue:         e.Session.Value,
		SessionMissingReason: e.Session.MissingReason,
		Stream:               e.Stream,
		Request: BodyMeta{
			ContentType:   e.Request.ContentType,
			CapturedBytes: e.Request.CapturedBytes,
			ObservedBytes: e.Request.ObservedBytes,
			Truncated:     e.Request.Truncated,
			Complete:      e.Request.Complete,
		},
		Response: BodyMeta{
			ContentType:   e.Response.ContentType,
			CapturedBytes: e.Response.CapturedBytes,
			ObservedBytes: e.Response.ObservedBytes,
			Truncated:     e.Response.Truncated,
			Complete:      e.Response.Complete,
		},
	}
}

func EncodeMetadata(meta Metadata) ([]byte, error) {
	encoded, err := common.Marshal(meta)
	if err != nil {
		return nil, invalidMetadata("metadata", "marshal failed: "+err.Error())
	}
	if len(encoded) > MaxMetadataBytes {
		return nil, invalidMetadata("metadata",
			fmt.Sprintf("encoded size %d bytes exceeds the %d byte limit", len(encoded), MaxMetadataBytes),
			ErrMetadataTooLarge)
	}
	return encoded, nil
}

func DecodeMetadata(data []byte) (Metadata, error) {
	if len(data) > MaxMetadataBytes {
		return Metadata{}, invalidMetadata("metadata",
			fmt.Sprintf("encoded size %d bytes exceeds the %d byte limit", len(data), MaxMetadataBytes),
			ErrMetadataTooLarge)
	}
	var meta Metadata
	if err := common.Unmarshal(data, &meta); err != nil {
		return Metadata{}, invalidMetadata("metadata", "unmarshal failed: "+err.Error())
	}
	return meta, nil
}

func ValidateMetadata(meta Metadata) error {
	if meta.Version != MetadataVersion {
		return invalidMetadata("version", fmt.Sprintf("must be %d, got %d", MetadataVersion, meta.Version))
	}
	required := []struct {
		field string
		value string
	}{
		{"site_id", meta.SiteID},
		{"storage_node_id", meta.StorageNodeID},
		{"job_id", meta.JobID},
		{"request_id", meta.RequestID},
		{"target_id", meta.TargetID},
		{"endpoint", meta.Endpoint},
	}
	for _, field := range required {
		if field.value == "" {
			return invalidMetadata(field.field, "must not be empty")
		}
	}
	if reason := siteLabelProblem(meta.SiteID); reason != "" {
		return invalidMetadata("site_id", reason, ErrInvalidSiteLabel)
	}
	if reason := jobIDProblem(meta.JobID); reason != "" {
		return invalidMetadata("job_id", reason, ErrInvalidJobID)
	}
	if meta.ConfigVersion < 1 {
		return invalidMetadata("config_version", fmt.Sprintf("must be at least 1, got %d", meta.ConfigVersion))
	}
	if meta.RequestStartedAt.IsZero() {
		return invalidMetadata("request_started_at", "must not be zero")
	}
	if meta.UserID < 0 {
		return invalidMetadata("user_id", "must not be negative", ErrInvalidUserID)
	}
	if meta.HTTPStatus != 0 && (meta.HTTPStatus < 100 || meta.HTTPStatus > 599) {
		return invalidMetadata("http_status", fmt.Sprintf("must be 0 or within [100, 599], got %d", meta.HTTPStatus))
	}
	if err := validateSessionFields(meta); err != nil {
		return err
	}
	if err := validateBodyMeta("request", meta.Request); err != nil {
		return err
	}
	if err := validateBodyMeta("response", meta.Response); err != nil {
		return err
	}
	if _, err := EncodeMetadata(meta); err != nil {
		return err
	}
	return nil
}

// EncodeEnvelope renders the section 5.2 on disk JSON; remote_path is recomputed from the metadata.
func EncodeEnvelope(meta Metadata, requestBody, responseBody []byte, frameSHA256 string) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteEnvelope(&buf, meta, [][]byte{requestBody}, [][]byte{responseBody}, frameSHA256); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WriteEnvelope base64 encodes each chunk straight into w so an 8 MiB body is never materialised twice.
func WriteEnvelope(w io.Writer, meta Metadata, requestChunks, responseChunks [][]byte, frameSHA256 string) error {
	if err := ValidateMetadata(meta); err != nil {
		return err
	}
	if err := ValidateFrameSHA256(frameSHA256); err != nil {
		return err
	}
	remotePath, err := meta.RemotePath()
	if err != nil {
		return err
	}
	if err := validateChunks("request", requestChunks, meta.Request); err != nil {
		return err
	}
	if err := validateChunks("response", responseChunks, meta.Response); err != nil {
		return err
	}
	head, err := common.Marshal(envelopeHead{
		Version:           meta.Version,
		JobID:             meta.JobID,
		RequestID:         meta.RequestID,
		UpstreamRequestID: meta.UpstreamRequestID,
		SiteID:            meta.SiteID,
		StorageNodeID:     meta.StorageNodeID,
		TargetID:          meta.TargetID,
		ConfigVersion:     meta.ConfigVersion,
		FrameSHA256:       frameSHA256,
		RemotePath:        remotePath,
		RequestStartedAt:  meta.RequestStartedAt,
		UserID:            meta.UserID,
		TokenID:           meta.TokenID,
		Channel:           EnvelopeChannel{ID: meta.ChannelID, Name: meta.ChannelName, Type: meta.ChannelType},
		Model:             meta.Model,
		Session: EnvelopeSession{
			Source:        meta.SessionSource,
			Value:         meta.SessionValue,
			MissingReason: meta.SessionMissingReason,
		},
		Endpoint:       meta.Endpoint,
		Stream:         meta.Stream,
		HTTPStatus:     meta.HTTPStatus,
		TerminalReason: meta.TerminalReason,
	})
	if err != nil {
		return envelopeError("envelope", "marshal head failed: "+err.Error())
	}
	if !bytes.HasSuffix(head, []byte("}")) {
		return envelopeError("envelope", "head fragment is not a json object")
	}
	requestOpen, requestClose, err := envelopeBodyFragments(meta.Request)
	if err != nil {
		return err
	}
	responseOpen, responseClose, err := envelopeBodyFragments(meta.Response)
	if err != nil {
		return err
	}

	out := &stickyWriter{w: w}
	out.Write(head[:len(head)-1])
	out.Write([]byte(`,"request":`))
	writeEnvelopeBody(out, requestOpen, requestChunks, requestClose)
	out.Write([]byte(`,"response":`))
	writeEnvelopeBody(out, responseOpen, responseChunks, responseClose)
	out.Write([]byte("}"))
	return out.err
}

func DecodeEnvelope(data []byte) (Envelope, error) {
	var envelope Envelope
	if err := common.Unmarshal(data, &envelope); err != nil {
		return Envelope{}, envelopeError("envelope", "unmarshal failed: "+err.Error())
	}
	return envelope, nil
}

func envelopeBodyFragments(body BodyMeta) ([]byte, []byte, error) {
	open, err := common.Marshal(envelopeBodyPrefix{ContentType: body.ContentType, Encoding: EnvelopeBodyEncoding})
	if err != nil {
		return nil, nil, envelopeError("envelope", "marshal body prefix failed: "+err.Error())
	}
	closed, err := common.Marshal(envelopeBodySuffix{
		CapturedBytes: body.CapturedBytes,
		ObservedBytes: body.ObservedBytes,
		Truncated:     body.Truncated,
		Complete:      body.Complete,
	})
	if err != nil {
		return nil, nil, envelopeError("envelope", "marshal body suffix failed: "+err.Error())
	}
	if !bytes.HasSuffix(open, []byte(`"}`)) || !bytes.HasPrefix(closed, []byte("{")) {
		return nil, nil, envelopeError("envelope", "unexpected body fragment shape")
	}
	return open[:len(open)-2], closed[1:], nil
}

func writeEnvelopeBody(out *stickyWriter, open []byte, chunks [][]byte, closed []byte) {
	out.Write(open)
	encoder := base64.NewEncoder(base64.StdEncoding, out)
	for _, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		if _, err := encoder.Write(chunk); err != nil && out.err == nil {
			out.err = err
		}
	}
	if err := encoder.Close(); err != nil && out.err == nil {
		out.err = err
	}
	out.Write([]byte(`",`))
	out.Write(closed)
}

func validateChunks(side string, chunks [][]byte, body BodyMeta) error {
	total := int64(0)
	for _, chunk := range chunks {
		total += int64(len(chunk))
	}
	if total != body.CapturedBytes {
		return envelopeError(side+".body",
			fmt.Sprintf("chunks hold %d bytes, metadata declares %d", total, body.CapturedBytes),
			ErrEnvelopeBody)
	}
	return nil
}

func validateSessionFields(meta Metadata) error {
	if meta.SessionValue != nil && *meta.SessionValue == "" {
		return invalidMetadata("session", "session_value must be nil or a non-empty value", ErrSessionInconsistent)
	}
	resolved := meta.SessionValue != nil
	if !resolved {
		if meta.SessionMissingReason == nil || *meta.SessionMissingReason == "" {
			return invalidMetadata("session", "a capture without a session value must record a missing reason", ErrSessionInconsistent)
		}
		return nil
	}
	if meta.SessionMissingReason != nil {
		return invalidMetadata("session", "a resolved session must not carry a missing reason", ErrSessionInconsistent)
	}
	if meta.SessionSource == nil || *meta.SessionSource == "" {
		return invalidMetadata("session", "a resolved session value must name its source", ErrSessionInconsistent)
	}
	if len(*meta.SessionValue) > SessionValueMaxBytes {
		return invalidMetadata("session",
			fmt.Sprintf("value is %d bytes, the limit is %d", len(*meta.SessionValue), SessionValueMaxBytes),
			ErrSessionInconsistent)
	}
	return nil
}

func validateBodyMeta(side string, body BodyMeta) error {
	if body.CapturedBytes < 0 {
		return invalidMetadata(side+".captured_bytes", fmt.Sprintf("must not be negative, got %d", body.CapturedBytes), ErrBodyBytes)
	}
	if body.ObservedBytes < 0 {
		return invalidMetadata(side+".observed_bytes", fmt.Sprintf("must not be negative, got %d", body.ObservedBytes), ErrBodyBytes)
	}
	if body.CapturedBytes > MaxBodyBytesPerSide {
		return invalidMetadata(side+".captured_bytes",
			fmt.Sprintf("%d bytes exceeds the %d byte per side limit", body.CapturedBytes, MaxBodyBytesPerSide), ErrBodyBytes)
	}
	if body.ObservedBytes < body.CapturedBytes {
		return invalidMetadata(side+".observed_bytes",
			fmt.Sprintf("%d is below the %d captured bytes", body.ObservedBytes, body.CapturedBytes), ErrBodyBytes)
	}
	if body.Truncated && body.ObservedBytes <= body.CapturedBytes {
		return invalidMetadata(side+".truncated",
			"a truncated side must have observed more bytes than it captured", ErrBodyBytes)
	}
	return nil
}

func invalidMetadata(field, reason string, extra ...error) error {
	errs := make([]error, 0, len(extra)+1)
	errs = append(errs, ErrInvalidMetadata)
	errs = append(errs, extra...)
	return &ValidationError{Field: field, Reason: reason, Errs: errs}
}

func envelopeError(field, reason string, extra ...error) error {
	errs := make([]error, 0, len(extra)+1)
	errs = append(errs, ErrInvalidEnvelope)
	errs = append(errs, extra...)
	return &ValidationError{Field: field, Reason: reason, Errs: errs}
}

type stickyWriter struct {
	w   io.Writer
	err error
}

func (s *stickyWriter) Write(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	n, err := s.w.Write(p)
	if err != nil {
		s.err = err
	}
	return n, err
}
