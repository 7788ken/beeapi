package contentbackup

import (
	"github.com/QuantumNous/new-api/common"
)

const (
	NoSessionKey         = "nosession"
	SessionValueMaxBytes = 1024

	SessionSourceMetadataUserID = "metadata.user_id"
	SessionSourcePromptCacheKey = "prompt_cache_key"
	SessionSourceUser           = "user"

	SessionMissingReasonAbsent      = "absent"
	SessionMissingReasonNotString   = "not_string"
	SessionMissingReasonTooLong     = "too_long"
	SessionMissingReasonUnparseable = "unparseable"
)

type SessionRef struct {
	Source        *string
	Value         *string
	MissingReason *string
}

func SessionSources() []string {
	return []string{SessionSourceMetadataUserID, SessionSourcePromptCacheKey, SessionSourceUser}
}

// SessionKey hashes the structured pair instead of source+value so "a"/"bc" and "ab"/"c" cannot collide.
func SessionKey(source, value string) string {
	if value == "" {
		return NoSessionKey
	}
	payload, err := common.Marshal([]string{source, value})
	if err != nil {
		return NoSessionKey
	}
	return common.Sha256(payload)
}

func MissingSession(reason string) SessionRef {
	return SessionRef{MissingReason: &reason}
}

// ResolveSession never coerces booleans, numbers, objects or arrays into a session value.
func ResolveSession(source string, raw any) SessionRef {
	if source == "" {
		return MissingSession(SessionMissingReasonAbsent)
	}
	value, ok := raw.(string)
	if !ok {
		if raw == nil {
			return MissingSession(SessionMissingReasonAbsent)
		}
		return MissingSession(SessionMissingReasonNotString)
	}
	if value == "" {
		return MissingSession(SessionMissingReasonAbsent)
	}
	if len(value) > SessionValueMaxBytes {
		return MissingSession(SessionMissingReasonTooLong)
	}
	resolvedSource := source
	resolvedValue := value
	return SessionRef{Source: &resolvedSource, Value: &resolvedValue}
}

func (m Metadata) SessionKey() string {
	if m.SessionValue == nil || *m.SessionValue == "" {
		return NoSessionKey
	}
	return SessionKey(common.DerefStringOr(m.SessionSource, ""), *m.SessionValue)
}
