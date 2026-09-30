package service

import (
	"errors"
	"fmt"

	"github.com/portfolio/whatsapp-support/internal/ai"
)

type AIProviderFailureKind string

const (
	AIProviderFailureAuthentication  AIProviderFailureKind = "authentication"
	AIProviderFailureInvalidRequest  AIProviderFailureKind = "invalid_request"
	AIProviderFailureRateLimited     AIProviderFailureKind = "rate_limited"
	AIProviderFailureUpstream        AIProviderFailureKind = "upstream"
	AIProviderFailureTransport       AIProviderFailureKind = "transport"
	AIProviderFailureInvalidResponse AIProviderFailureKind = "invalid_response"
)

// AIProviderFailure adapts provider-neutral failures to the application layer.
type AIProviderFailure struct {
	Kind AIProviderFailureKind
	Err  error
}

func (e *AIProviderFailure) Error() string {
	if e == nil || e.Err == nil {
		return "AI provider failure"
	}
	var providerErr *ai.ProviderError
	if errors.As(e.Err, &providerErr) {
		return fmt.Sprintf("AI provider %s failure: %v", e.Kind, providerErr)
	}
	return fmt.Sprintf("AI provider %s failure", e.Kind)
}

func (e *AIProviderFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newAIProviderFailure(err error) *AIProviderFailure {
	var providerErr *ai.ProviderError
	if !errors.As(err, &providerErr) {
		return &AIProviderFailure{Kind: AIProviderFailureUpstream, Err: err}
	}

	kind := AIProviderFailureUpstream
	switch providerErr.Kind {
	case ai.ProviderErrorAuthentication:
		kind = AIProviderFailureAuthentication
	case ai.ProviderErrorInvalidRequest:
		kind = AIProviderFailureInvalidRequest
	case ai.ProviderErrorRateLimited:
		kind = AIProviderFailureRateLimited
	case ai.ProviderErrorTransport:
		kind = AIProviderFailureTransport
	case ai.ProviderErrorInvalidResponse:
		kind = AIProviderFailureInvalidResponse
	case ai.ProviderErrorUpstream:
		kind = AIProviderFailureUpstream
	}
	return &AIProviderFailure{Kind: kind, Err: err}
}
