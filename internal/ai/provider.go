package ai

import "context"

type Input struct {
	Content string
	History []Message
}

type Message struct {
	Role    string
	Content string
}

type AIReply struct {
	Content       string
	RequiresHuman bool
}

type Provider interface {
	GenerateReply(context.Context, Input) (AIReply, error)
}
