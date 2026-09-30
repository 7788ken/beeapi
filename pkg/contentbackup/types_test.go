package contentbackup

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func strPtr(value string) *string {
	return &value
}

func validMetadata() Metadata {
	return Metadata{
		Version:              MetadataVersion,
		SiteID:               "ai",
		StorageNodeID:        "example-node-1",
		JobID:                "00000000-0000-4000-8000-000000000001",
		RequestID:            "example-gateway-request-id",
		TargetID:             "example-target-1",
		ConfigVersion:        1,
		RequestStartedAt:     time.Date(2026, 9, 15, 4, 34, 56, 789000000, time.UTC),
		UserID:               10086,
		TokenID:              42,
		ChannelID:            12,
		ChannelName:          "example",
		Model:                "example-model",
		Endpoint:             "/v1/chat/completions",
		TerminalReason:       "validation_error",
		ChannelType:          1,
		HTTPStatus:           400,
		SessionMissingReason: strPtr(SessionMissingReasonAbsent),
		Request:              BodyMeta{ContentType: "application/json", CapturedBytes: 2, ObservedBytes: 2, Complete: true},
		Response:             BodyMeta{ContentType: "application/json", CapturedBytes: 2, ObservedBytes: 2, Complete: true},
	}
}

func TestContentBackupProtocolConstants(t *testing.T) {
	if MetadataVersion != 1 {
		t.Fatalf("MetadataVersion = %d, want 1", MetadataVersion)
	}
	if MaxMetadataBytes != 64*1024 {
		t.Fatalf("MaxMetadataBytes = %d, want 64 KiB", MaxMetadataBytes)
	}
	if MaxBodyBytesPerSide != 8*1024*1024 {
		t.Fatalf("MaxBodyBytesPerSide = %d, want 8 MiB", MaxBodyBytesPerSide)
	}
	if EnvelopeBodyEncoding != "base64" {
		t.Fatalf("EnvelopeBodyEncoding = %q, want %q", EnvelopeBodyEncoding, "base64")
	}
	if RemoteFileExtension != ".json.gz" {
		t.Fatalf("RemoteFileExtension = %q, want %q", RemoteFileExtension, ".json.gz")
	}
	if DefaultConfig().MaxBodyBytes > MaxBodyBytesPerSide {
		t.Fatal("the configurable per-side body limit must never exceed the protocol cap")
	}
}

func TestContentBackupValidateMetadataAcceptsValid(t *testing.T) {
	if err := ValidateMetadata(validMetadata()); err != nil {
		t.Fatalf("ValidateMetadata(validMetadata()) = %v", err)
	}
	meta := validMetadata()
	meta.SessionSource = strPtr(SessionSourceMetadataUserID)
	meta.SessionValue = strPtr("claude-user-1")
	meta.SessionMissingReason = nil
	if err := ValidateMetadata(meta); err != nil {
		t.Fatalf("ValidateMetadata with a session = %v", err)
	}
	meta = validMetadata()
	meta.Request.CapturedBytes = MaxBodyBytesPerSide
	meta.Request.ObservedBytes = MaxBodyBytesPerSide + 4096
	meta.Request.Truncated = true
	meta.Response.CapturedBytes = 0
	meta.Response.ObservedBytes = 0
	meta.Response.Complete = false
	meta.Response.Truncated = false
	meta.Stream = true
	meta.HTTPStatus = 200
	if err := ValidateMetadata(meta); err != nil {
		t.Fatalf("ValidateMetadata with a truncated request = %v", err)
	}
	meta = validMetadata()
	meta.HTTPStatus = 0
	meta.UpstreamRequestID = strPtr("upstream-1")
	if err := ValidateMetadata(meta); err != nil {
		t.Fatalf("ValidateMetadata without an http status = %v", err)
	}
}

func TestContentBackupValidateMetadataRejectsInvalid(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Metadata)
		wantErr   error
		wantField string
	}{
		{name: "version zero", mutate: func(m *Metadata) { m.Version = 0 }, wantErr: ErrInvalidMetadata, wantField: "version"},
		{name: "unknown version", mutate: func(m *Metadata) { m.Version = 2 }, wantErr: ErrInvalidMetadata, wantField: "version"},
		{name: "empty site", mutate: func(m *Metadata) { m.SiteID = "" }, wantErr: ErrInvalidMetadata, wantField: "site_id"},
		{name: "site is not a label", mutate: func(m *Metadata) { m.SiteID = "AI" }, wantErr: ErrInvalidSiteLabel, wantField: "site_id"},
		{name: "empty storage node", mutate: func(m *Metadata) { m.StorageNodeID = "" }, wantErr: ErrInvalidMetadata, wantField: "storage_node_id"},
		{name: "empty job id", mutate: func(m *Metadata) { m.JobID = "" }, wantErr: ErrInvalidMetadata, wantField: "job_id"},
		{name: "job id is not a uuid", mutate: func(m *Metadata) { m.JobID = "../job" }, wantErr: ErrInvalidJobID, wantField: "job_id"},
		{name: "empty request id", mutate: func(m *Metadata) { m.RequestID = "" }, wantErr: ErrInvalidMetadata, wantField: "request_id"},
		{name: "empty target id", mutate: func(m *Metadata) { m.TargetID = "" }, wantErr: ErrInvalidMetadata, wantField: "target_id"},
		{name: "empty endpoint", mutate: func(m *Metadata) { m.Endpoint = "" }, wantErr: ErrInvalidMetadata, wantField: "endpoint"},
		{name: "config version zero", mutate: func(m *Metadata) { m.ConfigVersion = 0 }, wantErr: ErrInvalidMetadata, wantField: "config_version"},
		{name: "zero start time", mutate: func(m *Metadata) { m.RequestStartedAt = time.Time{} }, wantErr: ErrInvalidMetadata, wantField: "request_started_at"},
		{name: "negative user id", mutate: func(m *Metadata) { m.UserID = -1 }, wantErr: ErrInvalidUserID, wantField: "user_id"},
		{name: "http status below range", mutate: func(m *Metadata) { m.HTTPStatus = 99 }, wantErr: ErrInvalidMetadata, wantField: "http_status"},
		{name: "http status above range", mutate: func(m *Metadata) { m.HTTPStatus = 600 }, wantErr: ErrInvalidMetadata, wantField: "http_status"},
		{name: "request captured above cap", mutate: func(m *Metadata) { m.Request.CapturedBytes = MaxBodyBytesPerSide + 1 }, wantErr: ErrBodyBytes, wantField: "request.captured_bytes"},
		{name: "response captured above cap", mutate: func(m *Metadata) {
			m.Response.CapturedBytes = MaxBodyBytesPerSide + 1
			m.Response.ObservedBytes = MaxBodyBytesPerSide + 1
		}, wantErr: ErrBodyBytes, wantField: "response.captured_bytes"},
		{name: "negative captured bytes", mutate: func(m *Metadata) { m.Request.CapturedBytes = -1 }, wantErr: ErrBodyBytes, wantField: "request.captured_bytes"},
		{name: "observed below captured", mutate: func(m *Metadata) { m.Response.ObservedBytes = 1 }, wantErr: ErrBodyBytes, wantField: "response.observed_bytes"},
		{name: "truncated without dropped bytes", mutate: func(m *Metadata) { m.Request.Truncated = true }, wantErr: ErrBodyBytes, wantField: "request.truncated"},
		{name: "session value with missing reason", mutate: func(m *Metadata) {
			m.SessionSource = strPtr(SessionSourceUser)
			m.SessionValue = strPtr("u-1")
		}, wantErr: ErrSessionInconsistent, wantField: "session"},
		{name: "session value without source", mutate: func(m *Metadata) {
			m.SessionValue = strPtr("u-1")
		}, wantErr: ErrSessionInconsistent, wantField: "session"},
		{name: "no session and no reason", mutate: func(m *Metadata) { m.SessionMissingReason = nil }, wantErr: ErrSessionInconsistent, wantField: "session"},
		{name: "session value pointer to an empty string", mutate: func(m *Metadata) {
			m.SessionValue = strPtr("")
		}, wantErr: ErrSessionInconsistent, wantField: "session"},
		{name: "session value above the byte limit", mutate: func(m *Metadata) {
			m.SessionSource = strPtr(SessionSourceUser)
			m.SessionValue = strPtr(strings.Repeat("a", SessionValueMaxBytes+1))
			m.SessionMissingReason = nil
		}, wantErr: ErrSessionInconsistent, wantField: "session"},
		{name: "metadata above 64 KiB", mutate: func(m *Metadata) { m.Model = strings.Repeat("m", MaxMetadataBytes) }, wantErr: ErrMetadataTooLarge, wantField: "metadata"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meta := validMetadata()
			test.mutate(&meta)
			err := ValidateMetadata(meta)
			if err == nil {
				t.Fatal("ValidateMetadata accepted invalid metadata")
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if !errors.Is(err, ErrInvalidMetadata) {
				t.Fatalf("every metadata rejection must match ErrInvalidMetadata, got %v", err)
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error %v must be a *ValidationError", err)
			}
			if validation.Field != test.wantField {
				t.Fatalf("field = %q, want %q", validation.Field, test.wantField)
			}
			if validation.Reason == "" {
				t.Fatal("the error must carry a reason")
			}
		})
	}
}

func TestContentBackupMetadataEncoding(t *testing.T) {
	meta := validMetadata()
	meta.UpstreamRequestID = strPtr("upstream-1")
	meta.SessionSource = strPtr(SessionSourcePromptCacheKey)
	meta.SessionValue = strPtr("  Cache-Key/1  ")
	meta.SessionMissingReason = nil
	encoded, err := EncodeMetadata(meta)
	if err != nil {
		t.Fatalf("EncodeMetadata: %v", err)
	}
	for _, key := range []string{
		`"version":1`, `"site_id":"ai"`, `"storage_node_id":"example-node-1"`,
		`"job_id":"00000000-0000-4000-8000-000000000001"`, `"request_id":"example-gateway-request-id"`,
		`"target_id":"example-target-1"`, `"config_version":1`, `"request_started_at":"2026-09-15T04:34:56.789Z"`,
		`"user_id":10086`, `"token_id":42`, `"channel_id":12`, `"channel_name":"example"`, `"channel_type":1`,
		`"model":"example-model"`, `"endpoint":"/v1/chat/completions"`, `"terminal_reason":"validation_error"`,
		`"http_status":400`, `"upstream_request_id":"upstream-1"`, `"session_source":"prompt_cache_key"`,
		`"session_value":"  Cache-Key/1  "`, `"stream":false`,
		`"request":{"content_type":"application/json","captured_bytes":2,"observed_bytes":2,"truncated":false,"complete":true}`,
	} {
		if !strings.Contains(string(encoded), key) {
			t.Fatalf("frame metadata must contain %s, got %s", key, encoded)
		}
	}
	if !strings.Contains(string(encoded), `"session_missing_reason":null`) {
		t.Fatalf("an accepted session must serialise the missing reason as null, got %s", encoded)
	}
	decoded, err := DecodeMetadata(encoded)
	if err != nil {
		t.Fatalf("DecodeMetadata: %v", err)
	}
	if !decoded.RequestStartedAt.Equal(meta.RequestStartedAt) {
		t.Fatalf("request_started_at = %s, want %s", decoded.RequestStartedAt, meta.RequestStartedAt)
	}
	// Parsing an RFC3339 "Z" stamp may yield a fixed zone instead of time.UTC, so compare in UTC.
	decoded.RequestStartedAt = decoded.RequestStartedAt.UTC()
	normalised := meta
	normalised.RequestStartedAt = normalised.RequestStartedAt.UTC()
	if !reflect.DeepEqual(decoded, normalised) {
		t.Fatalf("metadata round trip changed the value:\n%#v\n%#v", decoded, normalised)
	}
	if decoded.SessionKey() != meta.SessionKey() {
		t.Fatal("the decoded metadata must derive the same session key")
	}
	path, err := decoded.RemotePath()
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	sessionKey := SessionKey(SessionSourcePromptCacheKey, "  Cache-Key/1  ")
	if strings.Contains(path, sessionKey) || strings.Contains(path, "nosession") {
		t.Fatalf("archive path must not carry the session key, got %q", path)
	}
	if !strings.Contains(path, "/"+decoded.JobID[:2]+"/"+decoded.JobID+".json.gz") {
		t.Fatalf("archive path must be the job-id shard, got %q", path)
	}
}

func TestContentBackupMetadataEncodingSizeLimit(t *testing.T) {
	meta := validMetadata()
	meta.Model = strings.Repeat("m", MaxMetadataBytes)
	_, err := EncodeMetadata(meta)
	if !errors.Is(err, ErrMetadataTooLarge) {
		t.Fatalf("EncodeMetadata error = %v, want %v", err, ErrMetadataTooLarge)
	}
	if err := ValidateMetadata(meta); !errors.Is(err, ErrMetadataTooLarge) {
		t.Fatalf("ValidateMetadata error = %v, want %v", err, ErrMetadataTooLarge)
	}
	if _, err := EncodeEnvelope(meta, []byte("{}"), []byte("{}"), newTestFrameSHA256()); !errors.Is(err, ErrMetadataTooLarge) {
		t.Fatalf("EncodeEnvelope error = %v, want %v", err, ErrMetadataTooLarge)
	}
	if _, err := DecodeMetadata(bytes.Repeat([]byte("m"), MaxMetadataBytes+1)); !errors.Is(err, ErrMetadataTooLarge) {
		t.Fatalf("DecodeMetadata error = %v, want %v", err, ErrMetadataTooLarge)
	}
	atLimit := validMetadata()
	atLimit.Model = ""
	base, err := EncodeMetadata(atLimit)
	if err != nil {
		t.Fatalf("EncodeMetadata: %v", err)
	}
	atLimit.Model = strings.Repeat("m", MaxMetadataBytes-len(base))
	encoded, err := EncodeMetadata(atLimit)
	if err != nil {
		t.Fatalf("metadata of exactly %d bytes must encode: %v", MaxMetadataBytes, err)
	}
	if len(encoded) != MaxMetadataBytes {
		t.Fatalf("encoded metadata is %d bytes, want exactly %d", len(encoded), MaxMetadataBytes)
	}
	atLimit.Model += "m"
	if _, err := EncodeMetadata(atLimit); !errors.Is(err, ErrMetadataTooLarge) {
		t.Fatalf("one byte over the limit must fail with %v, got %v", ErrMetadataTooLarge, err)
	}
}

func TestContentBackupEnvelopeMatchesDocumentShape(t *testing.T) {
	meta := validMetadata()
	body := []byte("{}")
	frameSHA256 := newTestFrameSHA256()
	encoded, err := EncodeEnvelope(meta, body, body, frameSHA256)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	encodedBody := base64.StdEncoding.EncodeToString(body)
	want := fmt.Sprintf(`{"version":1,"job_id":"00000000-0000-4000-8000-000000000001","request_id":"example-gateway-request-id","upstream_request_id":null,"site_id":"ai","storage_node_id":"example-node-1","target_id":"example-target-1","config_version":1,"frame_sha256":%q,"remote_path":"/ai/2026-09-15/00/00000000-0000-4000-8000-000000000001.json.gz","request_started_at":"2026-09-15T04:34:56.789Z","user_id":10086,"token_id":42,"channel":{"id":12,"name":"example","type":1},"model":"example-model","session":{"source":null,"value":null,"missing_reason":"absent"},"endpoint":"/v1/chat/completions","stream":false,"http_status":400,"terminal_reason":"validation_error","request":{"content_type":"application/json","encoding":"base64","body":%q,"captured_bytes":2,"observed_bytes":2,"truncated":false,"complete":true},"response":{"content_type":"application/json","encoding":"base64","body":%q,"captured_bytes":2,"observed_bytes":2,"truncated":false,"complete":true}}`,
		frameSHA256, encodedBody, encodedBody)
	if string(encoded) != want {
		t.Fatalf("envelope JSON does not match the section 5.2 shape\ngot  %s\nwant %s", encoded, want)
	}
	decoded, err := DecodeEnvelope(encoded)
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	remarshalled, err := common.Marshal(decoded)
	if err != nil {
		t.Fatalf("marshal decoded envelope: %v", err)
	}
	if !bytes.Equal(remarshalled, encoded) {
		t.Fatalf("the Envelope struct tags must reproduce the streamed bytes\ngot  %s\nwant %s", remarshalled, encoded)
	}
	rebuilt := decoded.Metadata()
	if !rebuilt.RequestStartedAt.Equal(meta.RequestStartedAt) {
		t.Fatalf("Envelope.Metadata start time = %s, want %s", rebuilt.RequestStartedAt, meta.RequestStartedAt)
	}
	rebuilt.RequestStartedAt = rebuilt.RequestStartedAt.UTC()
	normalised := meta
	normalised.RequestStartedAt = normalised.RequestStartedAt.UTC()
	if !reflect.DeepEqual(rebuilt, normalised) {
		t.Fatalf("Envelope.Metadata must rebuild the flat metadata:\n%#v\n%#v", rebuilt, normalised)
	}
	if decoded.FrameSHA256 != frameSHA256 {
		t.Fatalf("frame_sha256 = %q, want %q", decoded.FrameSHA256, frameSHA256)
	}
	if decoded.Request.Body != encodedBody || decoded.Response.Body != encodedBody {
		t.Fatalf("bodies must stay base64: %q / %q", decoded.Request.Body, decoded.Response.Body)
	}
}

func TestContentBackupEnvelopeSessionAndUpstreamNulls(t *testing.T) {
	meta := validMetadata()
	meta.UpstreamRequestID = strPtr("upstream-1")
	meta.SessionSource = strPtr(SessionSourceMetadataUserID)
	meta.SessionValue = strPtr("claude-user-1")
	meta.SessionMissingReason = nil
	encoded, err := EncodeEnvelope(meta, []byte("{}"), []byte("{}"), newTestFrameSHA256())
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	for _, key := range []string{
		`"upstream_request_id":"upstream-1"`,
		`"session":{"source":"metadata.user_id","value":"claude-user-1","missing_reason":null}`,
	} {
		if !strings.Contains(string(encoded), key) {
			t.Fatalf("envelope must contain %s, got %s", key, encoded)
		}
	}
	sessionKey := SessionKey(SessionSourceMetadataUserID, "claude-user-1")
	path, err := meta.RemotePath()
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	if !strings.Contains(string(encoded), `"remote_path":"`+path+`"`) {
		t.Fatalf("the envelope remote_path must be recomputed from the metadata: %s", encoded)
	}
	if strings.Contains(path, sessionKey) {
		t.Fatalf("remote path %q must leave the session hash %q in the envelope, not the path", path, sessionKey)
	}
	decoded, err := DecodeEnvelope(encoded)
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	if decoded.Session.Source == nil || *decoded.Session.Source != SessionSourceMetadataUserID {
		t.Fatalf("decoded session source = %v", decoded.Session.Source)
	}
	if decoded.Session.MissingReason != nil {
		t.Fatalf("decoded missing reason must stay null, got %q", *decoded.Session.MissingReason)
	}
	if decoded.UpstreamRequestID == nil || *decoded.UpstreamRequestID != "upstream-1" {
		t.Fatalf("decoded upstream request id = %v", decoded.UpstreamRequestID)
	}
}

func TestContentBackupWriteEnvelopeStreamsChunks(t *testing.T) {
	meta := validMetadata()
	requestChunks := [][]byte{{0x00, 0xff, 0x80}, []byte("会"), nil, []byte(`{"a":1}`)}
	responseChunks := [][]byte{[]byte("data: {\"id\":\"1\"}\n\n"), []byte("data: [DONE]\n\n")}
	meta.Request.ContentType = "application/octet-stream"
	meta.Request.CapturedBytes = int64(chunksLen(requestChunks))
	meta.Request.ObservedBytes = meta.Request.CapturedBytes + 16
	meta.Request.Truncated = true
	meta.Response.ContentType = "text/event-stream"
	meta.Response.CapturedBytes = int64(chunksLen(responseChunks))
	meta.Response.ObservedBytes = meta.Response.CapturedBytes
	meta.Response.Complete = true
	meta.Stream = true
	meta.HTTPStatus = 200
	meta.SessionSource = strPtr(SessionSourceUser)
	meta.SessionValue = strPtr("  Cache-Key/1  ")
	meta.SessionMissingReason = nil
	if err := ValidateMetadata(meta); err != nil {
		t.Fatalf("ValidateMetadata: %v", err)
	}
	frameSHA256 := newTestFrameSHA256()
	var streamed bytes.Buffer
	if err := WriteEnvelope(&streamed, meta, requestChunks, responseChunks, frameSHA256); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	whole, err := EncodeEnvelope(meta, joinChunks(requestChunks), joinChunks(responseChunks), frameSHA256)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	if !bytes.Equal(streamed.Bytes(), whole) {
		t.Fatalf("chunked streaming must match the contiguous encoding\ngot  %s\nwant %s", streamed.Bytes(), whole)
	}
	wantRequest := `"body":"` + base64.StdEncoding.EncodeToString(joinChunks(requestChunks)) + `"`
	if !strings.Contains(streamed.String(), wantRequest) {
		t.Fatalf("request body must be the base64 of every chunk in order, want %s in %s", wantRequest, streamed.String())
	}
	if strings.Contains(streamed.String(), `"body":""`) {
		t.Fatalf("a non empty body must not serialise as empty: %s", streamed.String())
	}
}

func TestContentBackupEnvelopeRejectsInconsistentInput(t *testing.T) {
	frameSHA256 := newTestFrameSHA256()
	shortBody := []byte("{}")
	tests := []struct {
		name      string
		meta      Metadata
		request   []byte
		response  []byte
		frameHash string
		wantErr   error
	}{
		{
			name:     "request body shorter than captured bytes",
			meta:     validMetadata(),
			request:  nil,
			response: shortBody,
			wantErr:  ErrEnvelopeBody,
		},
		{
			name:     "response body longer than captured bytes",
			meta:     validMetadata(),
			request:  shortBody,
			response: []byte("{}{}"),
			wantErr:  ErrEnvelopeBody,
		},
		{
			name:      "frame hash is not 64 lowercase hex",
			meta:      validMetadata(),
			request:   shortBody,
			response:  shortBody,
			frameHash: strings.ToUpper(frameSHA256),
			wantErr:   ErrInvalidFrameSHA256,
		},
		{
			name:      "empty frame hash",
			meta:      validMetadata(),
			request:   shortBody,
			response:  shortBody,
			frameHash: "",
			wantErr:   ErrInvalidFrameSHA256,
		},
		{
			name: "invalid metadata is rejected before encoding",
			meta: func() Metadata {
				broken := validMetadata()
				broken.JobID = "../job"
				return broken
			}(),
			request:  shortBody,
			response: shortBody,
			wantErr:  ErrInvalidJobID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hash := test.frameHash
			if hash == "" && test.wantErr != ErrInvalidFrameSHA256 {
				hash = frameSHA256
			}
			if _, err := EncodeEnvelope(test.meta, test.request, test.response, hash); !errors.Is(err, test.wantErr) {
				t.Fatalf("EncodeEnvelope error = %v, want %v", err, test.wantErr)
			}
			var buf bytes.Buffer
			err := WriteEnvelope(&buf, test.meta, [][]byte{test.request}, [][]byte{test.response}, hash)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("WriteEnvelope error = %v, want %v", err, test.wantErr)
			}
			if err != nil && buf.Len() != 0 {
				t.Fatalf("a rejected envelope must not be partially written, got %d bytes", buf.Len())
			}
		})
	}
}

func TestContentBackupEnvelopeTimeSerialisation(t *testing.T) {
	meta := validMetadata()
	meta.RequestStartedAt = time.Date(2026, 9, 15, 4, 34, 56, 123456789, time.UTC)
	encoded, err := EncodeMetadata(meta)
	if err != nil {
		t.Fatalf("EncodeMetadata: %v", err)
	}
	if !strings.Contains(string(encoded), `"request_started_at":"2026-09-15T04:34:56.123456789Z"`) {
		t.Fatalf("request_started_at must use RFC3339Nano, got %s", encoded)
	}
	offset := time.FixedZone("UTC-5", -5*3600)
	meta.RequestStartedAt = time.Date(2026, 9, 14, 23, 34, 56, 123456789, offset)
	decoded, err := DecodeMetadata(mustEncode(t, meta))
	if err != nil {
		t.Fatalf("DecodeMetadata: %v", err)
	}
	if !decoded.RequestStartedAt.Equal(meta.RequestStartedAt) {
		t.Fatalf("the decoded instant = %s, want %s", decoded.RequestStartedAt, meta.RequestStartedAt)
	}
	path, err := decoded.RemotePath()
	if err != nil {
		t.Fatalf("RemotePath: %v", err)
	}
	if !strings.Contains(path, "/2026-09-15/") {
		t.Fatalf("2026-09-14T23:34:56-05:00 is 2026-09-15 in beijing, got %q", path)
	}
}

func TestContentBackupCaptureShape(t *testing.T) {
	released := 0
	capture := &Capture{
		Meta:           validMetadata(),
		RequestChunks:  [][]byte{[]byte("{}")},
		ResponseChunks: [][]byte{[]byte("{}")},
		Release:        func() { released++ },
	}
	if capture.Release == nil {
		t.Fatal("Capture must carry its own release callback")
	}
	capture.Release()
	if released != 1 {
		t.Fatalf("Release ran %d times, want exactly 1", released)
	}
	if chunksLen(capture.RequestChunks) != int(capture.Meta.Request.CapturedBytes) {
		t.Fatalf("request chunks hold %d bytes, metadata declares %d", chunksLen(capture.RequestChunks), capture.Meta.Request.CapturedBytes)
	}
	var buf bytes.Buffer
	if err := WriteEnvelope(&buf, capture.Meta, capture.RequestChunks, capture.ResponseChunks, newTestFrameSHA256()); err != nil {
		t.Fatalf("WriteEnvelope from a capture: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("the envelope must not be empty")
	}
}

func TestContentBackupValidationErrorUnwrap(t *testing.T) {
	err := ValidateMetadata(Metadata{})
	if err == nil {
		t.Fatal("zero metadata must be invalid")
	}
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error %v must be a *ValidationError", err)
	}
	if !strings.Contains(err.Error(), validation.Field) {
		t.Fatalf("the message %q must name the field %q", err.Error(), validation.Field)
	}
	if len(validation.Errs) == 0 {
		t.Fatal("a validation error must carry at least one sentinel")
	}
}

func mustEncode(t *testing.T, meta Metadata) []byte {
	t.Helper()
	encoded, err := EncodeMetadata(meta)
	if err != nil {
		t.Fatalf("EncodeMetadata: %v", err)
	}
	return encoded
}

func newTestFrameSHA256() string {
	return common.Sha256([]byte("synthetic-frame-bytes"))
}

func chunksLen(chunks [][]byte) int {
	total := 0
	for _, chunk := range chunks {
		total += len(chunk)
	}
	return total
}

func joinChunks(chunks [][]byte) []byte {
	var buf bytes.Buffer
	for _, chunk := range chunks {
		buf.Write(chunk)
	}
	return buf.Bytes()
}
