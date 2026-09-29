package ai

import (
	"context"
	"net/http"
	"testing"
)

func TestNewProviderDefaultsToLocal(t *testing.T) {
	provider, err := NewProvider(Config{}, nil)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if _, ok := provider.(LocalProvider); !ok {
		t.Fatalf("NewProvider() type = %T, want LocalProvider", provider)
	}
}

func TestLocalProviderStillReturnsItsConfiguredReply(t *testing.T) {
	reply, err := (LocalProvider{}).GenerateReply(context.Background(), Input{Content: "mensagem de teste"})
	if err != nil {
		t.Fatalf("GenerateReply() error = %v", err)
	}
	if reply.Content != "Olá! Recebi sua mensagem. Como posso ajudar?" || reply.RequiresHuman {
		t.Fatalf("local reply = %+v, unexpected response", reply)
	}
}

func TestNewProviderSelectsOpenAI(t *testing.T) {
	client := &http.Client{}
	provider, err := NewProvider(Config{Name: "openai", OpenAIKey: "test-key", OpenAIModel: "gpt-6-luna"}, client)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	openAIProvider, ok := provider.(*OpenAIProvider)
	if !ok {
		t.Fatalf("NewProvider() type = %T, want *OpenAIProvider", provider)
	}
	if openAIProvider.client != client {
		t.Fatal("NewProvider() did not retain configured HTTP client")
	}
}

func TestNewProviderRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{name: "unknown provider", config: Config{Name: "other"}},
		{name: "missing API key", config: Config{Name: "openai", OpenAIModel: "gpt-6-luna"}},
		{name: "missing model", config: Config{Name: "openai", OpenAIKey: "test-key"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewProvider(tt.config, nil); err == nil {
				t.Fatal("NewProvider() error = nil, want configuration error")
			}
		})
	}
}
