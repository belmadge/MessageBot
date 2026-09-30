package ai

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

type ProviderErrorKind string

const (
	ProviderErrorAuthentication  ProviderErrorKind = "authentication"
	ProviderErrorInvalidRequest  ProviderErrorKind = "invalid_request"
	ProviderErrorRateLimited     ProviderErrorKind = "rate_limited"
	ProviderErrorUpstream        ProviderErrorKind = "upstream"
	ProviderErrorTransport       ProviderErrorKind = "transport"
	ProviderErrorInvalidResponse ProviderErrorKind = "invalid_response"
)

var (
	ErrProviderAuthentication  = errors.New("AI provider authentication failed")
	ErrProviderInvalidRequest  = errors.New("AI provider rejected the request")
	ErrProviderRateLimited     = errors.New("AI provider is rate limited")
	ErrProviderUpstream        = errors.New("AI provider upstream failure")
	ErrProviderTransport       = errors.New("AI provider transport failure")
	ErrProviderInvalidResponse = errors.New("AI provider returned an invalid response")
)

const providerDiagnosticLimit = 256

// ProviderError holds provider-neutral classification and bounded diagnostics.
// It deliberately does not retain the complete upstream response body.
type ProviderError struct {
	Kind       ProviderErrorKind
	StatusCode int
	Code       string
	Message    string
	Err        error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "AI provider failure"
	}
	detail := fmt.Sprintf("AI provider %s failure", e.Kind)
	if e.StatusCode != 0 {
		detail += fmt.Sprintf(" (HTTP %d)", e.StatusCode)
	}
	if e.Code != "" {
		detail += " code=" + e.Code
	}
	if e.Message != "" {
		detail += ": " + e.Message
	}
	return detail
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ProviderError) Is(target error) bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ProviderErrorAuthentication:
		return target == ErrProviderAuthentication
	case ProviderErrorInvalidRequest:
		return target == ErrProviderInvalidRequest
	case ProviderErrorRateLimited:
		return target == ErrProviderRateLimited
	case ProviderErrorUpstream:
		return target == ErrProviderUpstream
	case ProviderErrorTransport:
		return target == ErrProviderTransport
	case ProviderErrorInvalidResponse:
		return target == ErrProviderInvalidResponse
	default:
		return false
	}
}

func classifyProviderStatus(status int) ProviderErrorKind {
	switch status {
	case 400:
		return ProviderErrorInvalidRequest
	case 401, 403:
		return ProviderErrorAuthentication
	case 429:
		return ProviderErrorRateLimited
	default:
		return ProviderErrorUpstream
	}
}

func sanitizeProviderDiagnostic(value, secret string, limit int) string {
	if secret != "" {
		value = strings.ReplaceAll(value, secret, "[redacted]")
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "..."
	}
	return value
}
