package ai

import "context"

// LocalProvider returns a predictable response for local demonstrations.
// It intentionally contains no routing or conversation rules.
type LocalProvider struct{}

func (LocalProvider) GenerateReply(_ context.Context, _ Input) (AIReply, error) {
	return AIReply{Content: "Olá! Recebi sua mensagem. Como posso ajudar?"}, nil
}
