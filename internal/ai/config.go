package ai

import (
	"errors"
	"net/http"
	"strings"
)

type Config struct {
	Name        string
	OpenAIKey   string
	OpenAIModel string
}

func NewProvider(config Config, client *http.Client) (Provider, error) {
	switch name := strings.ToLower(strings.TrimSpace(config.Name)); name {
	case "", "local":
		return LocalProvider{}, nil
	case "openai":
		if strings.TrimSpace(config.OpenAIKey) == "" {
			return nil, errors.New("OPENAI_API_KEY is required when AI_PROVIDER=openai")
		}
		if strings.TrimSpace(config.OpenAIModel) == "" {
			return nil, errors.New("OPENAI_MODEL is required when AI_PROVIDER=openai")
		}
		if client == nil {
			client = http.DefaultClient
		}
		return newOpenAIProvider(config.OpenAIKey, config.OpenAIModel, client, openAIResponsesURL), nil
	default:
		return nil, errors.New("AI_PROVIDER must be local or openai")
	}
}
