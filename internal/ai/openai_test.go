package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIProviderSendsStructuredResponseRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Errorf("request = %s %s, want POST /v1/responses", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		var request struct {
			Model        string `json:"model"`
			Instructions string `json:"instructions"`
			Input        []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"input"`
			Text struct {
				Format struct {
					Type   string `json:"type"`
					Strict bool   `json:"strict"`
				} `json:"format"`
			} `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if request.Model != "gpt-6-luna" || len(request.Input) != 3 {
			t.Errorf("request model/input length = %q/%d", request.Model, len(request.Input))
		}
		if !strings.Contains(request.Instructions, "conteúdo não confiável") {
			t.Error("instructions must keep the rule that conversation history is untrusted content")
		}
		wantInput := []struct{ role, content string }{
			{"user", "Olá, ação 😊"},
			{"assistant", "Ação concluída, obrigada!"},
			{"user", "Preciso falar com alguém"},
		}
		for i, want := range wantInput {
			if i >= len(request.Input) {
				t.Errorf("input[%d] missing, want role=%q content=%q", i, want.role, want.content)
				continue
			}
			if request.Input[i].Role != want.role || request.Input[i].Content != want.content {
				t.Errorf("input[%d] = %+v, want role=%q content=%q", i, request.Input[i], want.role, want.content)
			}
		}
		if request.Text.Format.Type != "json_schema" || !request.Text.Format.Strict {
			t.Errorf("response format = %+v, want strict json_schema", request.Text.Format)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"content\":\"Vou encaminhar você.\",\"requires_human\":true}"}]}]}`)
	}))
	defer server.Close()

	provider := newOpenAIProvider("test-key", "gpt-6-luna", server.Client(), server.URL+"/v1/responses")
	reply, err := provider.GenerateReply(context.Background(), Input{
		Content: "Preciso falar com alguém",
		History: []Message{{Role: "user", Content: "Olá, ação 😊"}, {Role: "assistant", Content: "Ação concluída, obrigada!"}},
	})
	if err != nil {
		t.Fatalf("GenerateReply() error = %v", err)
	}
	if reply.Content != "Vou encaminhar você." || !reply.RequiresHuman {
		t.Fatalf("GenerateReply() = %+v, want human handoff reply", reply)
	}
}

func TestOpenAIProviderClassifiesHTTPFailuresWithLimitedDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		status int
		kind   ProviderErrorKind
		code   string
	}{
		{name: "invalid request", status: http.StatusBadRequest, kind: ProviderErrorInvalidRequest, code: "invalid_request"},
		{name: "unauthorized", status: http.StatusUnauthorized, kind: ProviderErrorAuthentication, code: "invalid_api_key"},
		{name: "forbidden", status: http.StatusForbidden, kind: ProviderErrorAuthentication, code: "access_denied"},
		{name: "quota", status: http.StatusTooManyRequests, kind: ProviderErrorRateLimited, code: "credit_balance_exhausted"},
		{name: "upstream", status: http.StatusBadGateway, kind: ProviderErrorUpstream, code: "server_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprintf(w, `{"error":{"message":"%s","type":"provider_error","code":"%s","extra":"must-not-be-retained"}}`, strings.Repeat("diagnostic ", 40)+"secret-test-key", tt.code)
			}))
			defer server.Close()

			provider := newOpenAIProvider("secret-test-key", "gpt-6-luna", server.Client(), server.URL)
			_, err := provider.GenerateReply(context.Background(), Input{Content: "Olá"})
			if err == nil {
				t.Fatal("GenerateReply() error = nil, want HTTP error")
			}
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) {
				t.Fatalf("error type = %T, want *ProviderError", err)
			}
			if providerErr.StatusCode != tt.status || providerErr.Kind != tt.kind || providerErr.Code != tt.code {
				t.Fatalf("provider error = %+v, want status=%d kind=%s code=%s", providerErr, tt.status, tt.kind, tt.code)
			}
			if len([]rune(providerErr.Message)) > providerDiagnosticLimit+3 {
				t.Errorf("diagnostic message length = %d, exceeds configured limit", len([]rune(providerErr.Message)))
			}
			if strings.Contains(err.Error(), "secret-test-key") || strings.Contains(err.Error(), "must-not-be-retained") {
				t.Errorf("error retained secret or full response body: %q", err)
			}
			if sentinel := providerErrorSentinel(tt.kind); !errors.Is(err, sentinel) {
				t.Errorf("errors.Is(error, %v) = false", sentinel)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestOpenAIProviderPreservesTransportError(t *testing.T) {
	cause := errors.New("synthetic dial failure")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, cause
	})}
	provider := newOpenAIProvider("test-key", "gpt-6-luna", client, "https://example.invalid/v1/responses")
	_, err := provider.GenerateReply(context.Background(), Input{Content: "hello"})
	if !errors.Is(err, ErrProviderTransport) || !errors.Is(err, cause) {
		t.Fatalf("error %v did not preserve provider and transport causes", err)
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != ProviderErrorTransport {
		t.Fatalf("error = %T, want transport *ProviderError", err)
	}
}

func TestOpenAIProviderReturnsErrorForMalformedStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"not-json"}]}]}`)
	}))
	defer server.Close()

	provider := newOpenAIProvider("test-key", "gpt-6-luna", server.Client(), server.URL)
	_, err := provider.GenerateReply(context.Background(), Input{Content: "Olá"})
	if err == nil {
		t.Fatal("GenerateReply() error = nil, want structured output error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != ProviderErrorInvalidResponse {
		t.Fatalf("error = %T, want invalid response *ProviderError", err)
	}
	if !errors.Is(err, ErrProviderInvalidResponse) {
		t.Fatal("errors.Is(error, ErrProviderInvalidResponse) = false")
	}
}

func providerErrorSentinel(kind ProviderErrorKind) error {
	switch kind {
	case ProviderErrorAuthentication:
		return ErrProviderAuthentication
	case ProviderErrorInvalidRequest:
		return ErrProviderInvalidRequest
	case ProviderErrorRateLimited:
		return ErrProviderRateLimited
	case ProviderErrorUpstream:
		return ErrProviderUpstream
	case ProviderErrorTransport:
		return ErrProviderTransport
	case ProviderErrorInvalidResponse:
		return ErrProviderInvalidResponse
	default:
		return nil
	}
}
