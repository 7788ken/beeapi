package common

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// NormalizeOrigin validates and canonicalizes a browser origin for exact
// scheme, host, and effective-port comparisons.
func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || strings.ContainsAny(raw, "\r\n") {
		return "", fmt.Errorf("origin is empty or invalid")
	}

	parsedURL, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid origin: %w", err)
	}
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("origin scheme must be http or https")
	}
	if parsedURL.Host == "" ||
		parsedURL.User != nil ||
		parsedURL.RawQuery != "" ||
		parsedURL.ForceQuery ||
		parsedURL.Fragment != "" ||
		strings.Contains(raw, "#") ||
		(parsedURL.Path != "" && parsedURL.Path != "/") {
		return "", fmt.Errorf("origin must contain only scheme and host")
	}

	hostname := strings.ToLower(parsedURL.Hostname())
	if hostname == "" || strings.Contains(hostname, "*") || strings.HasSuffix(parsedURL.Host, ":") {
		return "", fmt.Errorf("origin host is invalid")
	}

	port := parsedURL.Port()
	if port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", fmt.Errorf("origin port is invalid")
		}
	}
	normalizedHost := hostname
	if strings.Contains(hostname, ":") {
		normalizedHost = "[" + hostname + "]"
	}
	if port == "" ||
		(scheme == "http" && port == "80") ||
		(scheme == "https" && port == "443") {
		return scheme + "://" + normalizedHost, nil
	}

	return scheme + "://" + net.JoinHostPort(hostname, port), nil
}

func InitSessionCookieSettings() error {
	secureRaw := strings.TrimSpace(os.Getenv("SESSION_COOKIE_SECURE"))
	trustedURLsRaw := strings.TrimSpace(os.Getenv("SESSION_COOKIE_TRUSTED_URL"))

	SessionCookieSecure = false
	SessionCookieTrustedURLs = nil

	if secureRaw == "" || strings.EqualFold(secureRaw, "false") {
		if trustedURLsRaw != "" {
			return fmt.Errorf("SESSION_COOKIE_TRUSTED_URL requires SESSION_COOKIE_SECURE=true")
		}
		return nil
	}
	if !strings.EqualFold(secureRaw, "true") {
		return fmt.Errorf("SESSION_COOKIE_SECURE must be true or false")
	}
	if trustedURLsRaw == "" {
		return fmt.Errorf("SESSION_COOKIE_SECURE=true requires SESSION_COOKIE_TRUSTED_URL")
	}

	rawTrustedURLs := strings.Split(trustedURLsRaw, ",")
	trustedURLs := make([]string, 0, len(rawTrustedURLs))
	for _, trustedURL := range rawTrustedURLs {
		trustedURL = strings.TrimSpace(trustedURL)
		if trustedURL == "" {
			return fmt.Errorf("SESSION_COOKIE_TRUSTED_URL contains an empty URL")
		}

		normalizedOrigin, err := NormalizeOrigin(trustedURL)
		if err != nil {
			return fmt.Errorf("invalid SESSION_COOKIE_TRUSTED_URL: %w", err)
		}
		if !strings.HasPrefix(normalizedOrigin, "https://") {
			return fmt.Errorf("SESSION_COOKIE_TRUSTED_URL must contain only https origins")
		}
		trustedURLs = append(trustedURLs, normalizedOrigin)
	}

	SessionCookieSecure = true
	SessionCookieTrustedURLs = trustedURLs
	return nil
}
