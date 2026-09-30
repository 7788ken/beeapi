package contentbackup

import (
	"bytes"
	"net"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Brand tokens are matched case-insensitively, longest first, then rewritten
// with MaskLabel: BAPI → BXXXI, ExampleBrand → EXXXd.
// Replace examplebrand with the names you actually need to strip.
var contentBackupBrandPattern = regexp.MustCompile(`(?i)examplebrand|beeapi|bapi`)

// IPv4 with an optional :port. Digits stay only at the first two and last two
// positions: 203.0.113.10 → 20x.x.xxx.10.
var contentBackupIPv4Pattern = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1?\d?\d)\.){3}(?:25[0-5]|2[0-4]\d|1?\d?\d)(?::\d{1,5})?\b`)

// Bracketed IPv6, with optional :port after the closing bracket.
var contentBackupIPv6BracketPattern = regexp.MustCompile(`\[[0-9a-fA-F:.]{2,}\](?::\d{1,5})?`)

// Bare IPv6 (at least two colons). Validated with net.ParseIP before rewrite.
var contentBackupIPv6BarePattern = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}\b`)

// MaskLabel keeps the first and last rune and puts a fixed XXX in the middle.
// One-rune input repeats that rune on both sides so it still looks redacted.
func MaskLabel(s string) string {
	if s == "" {
		return s
	}
	first, firstSize := utf8.DecodeRuneInString(s)
	if first == utf8.RuneError && firstSize == 1 {
		return "XXX"
	}
	last, lastSize := utf8.DecodeLastRuneInString(s)
	if last == utf8.RuneError && lastSize == 1 {
		return string(first) + "XXX"
	}
	return string(first) + "XXX" + string(last)
}

// RedactText masks brand tokens and IP addresses in place. Length may change
// (BAPI is 4 runes, BXXXI is 5); callers must refresh byte counters.
func RedactText(s string) string {
	if s == "" {
		return s
	}
	s = contentBackupBrandPattern.ReplaceAllStringFunc(s, MaskLabel)
	s = contentBackupIPv4Pattern.ReplaceAllStringFunc(s, maskIPv4Match)
	s = contentBackupIPv6BracketPattern.ReplaceAllStringFunc(s, maskIPv6BracketMatch)
	s = contentBackupIPv6BarePattern.ReplaceAllStringFunc(s, maskIPv6BareMatch)
	return s
}

// RedactBytes is RedactText over a raw archive body.
func RedactBytes(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	redacted := RedactText(string(data))
	if redacted == string(data) {
		return data
	}
	return []byte(redacted)
}

// RedactChunks joins then redacts so a brand/IP split across two blocks is
// still rewritten. Unchanged input is returned as-is to keep existing slice
// identity for tests that compare pointers after a no-op.
func RedactChunks(chunks [][]byte) [][]byte {
	if len(chunks) == 0 {
		return chunks
	}
	joined := bytes.Join(chunks, nil)
	redacted := RedactBytes(joined)
	if bytes.Equal(joined, redacted) {
		return chunks
	}
	return [][]byte{redacted}
}

func maskIPv4Match(match string) string {
	host, port := splitHostPortSuffix(match)
	return maskIPv4Host(host) + port
}

func maskIPv4Host(host string) string {
	totalDigits := 0
	for _, r := range host {
		if r >= '0' && r <= '9' {
			totalDigits++
		}
	}
	if totalDigits == 0 {
		return host
	}
	keepHead, keepTail := 2, 2
	if totalDigits <= keepHead+keepTail {
		keepHead = 1
		if totalDigits <= 2 {
			keepTail = min(1, totalDigits-keepHead)
		} else {
			keepTail = totalDigits - keepHead
		}
	}
	var b strings.Builder
	b.Grow(len(host))
	seen := 0
	for _, r := range host {
		if r >= '0' && r <= '9' {
			if seen < keepHead || seen >= totalDigits-keepTail {
				b.WriteRune(r)
			} else {
				b.WriteByte('x')
			}
			seen++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func maskIPv6BracketMatch(match string) string {
	closeAt := strings.IndexByte(match, ']')
	if closeAt < 1 {
		return match
	}
	inner := match[1:closeAt]
	suffix := match[closeAt+1:] // empty or :port
	masked, ok := maskIPv6Host(inner)
	if !ok {
		return match
	}
	return "[" + masked + "]" + suffix
}

func maskIPv6BareMatch(match string) string {
	if ip := net.ParseIP(match); ip != nil && ip.To4() != nil {
		return maskIPv4Host(ip.String())
	}
	masked, ok := maskIPv6Host(match)
	if !ok {
		return match
	}
	return masked
}

func maskIPv6Host(host string) (string, bool) {
	ip := net.ParseIP(host)
	if ip == nil {
		return "", false
	}
	if v4 := ip.To4(); v4 != nil {
		return maskIPv4Host(v4.String()), true
	}
	ip = ip.To16()
	if ip == nil {
		return "", false
	}
	groups := make([]string, 8)
	for i := 0; i < 8; i++ {
		groups[i] = trimIPv6Group(int(ip[i*2])<<8 | int(ip[i*2+1]))
	}
	for i := 2; i < 6; i++ {
		groups[i] = "x"
	}
	return strings.Join(groups, ":"), true
}

func trimIPv6Group(n int) string {
	if n == 0 {
		return "0"
	}
	const hexdigits = "0123456789abcdef"
	var buf [4]byte
	i := 4
	for n > 0 {
		i--
		buf[i] = hexdigits[n&0xf]
		n >>= 4
	}
	return string(buf[i:])
}

func splitHostPortSuffix(match string) (host, port string) {
	colon := strings.LastIndexByte(match, ':')
	if colon <= 0 {
		return match, ""
	}
	// IPv4:port has exactly one colon.
	if strings.Count(match, ":") != 1 {
		return match, ""
	}
	return match[:colon], match[colon:]
}

// IsProbeUserText reports whether a single user-visible utterance is a
// health-check ping rather than a real prompt.
func IsProbeUserText(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range strings.ToLower(trimmed) {
		if unicode.IsSpace(r) {
			if b.Len() > 0 && b.String()[b.Len()-1] != ' ' {
				b.WriteByte(' ')
			}
			continue
		}
		if unicode.IsPunct(r) {
			continue
		}
		b.WriteRune(r)
	}
	switch strings.TrimSpace(b.String()) {
	case "hi", "hello", "hello world", "ping", "pong", "test", "ok", "yes", "1",
		"你好", "测试", "在吗", "活着":
		return true
	}
	return false
}
