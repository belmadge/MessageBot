package service

import "time"

type Status string

const (
	StatusBot      Status = "bot"
	StatusHuman    Status = "human"
	StatusResolved Status = "resolved"
)

type Contact struct {
	ID        int64     `json:"id"`
	Phone     string    `json:"phone"`
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Conversation struct {
	ID        int64     `json:"id"`
	ContactID int64     `json:"contact_id"`
	Phone     string    `json:"phone"`
	Status    Status    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             int64     `json:"id"`
	ConversationID int64     `json:"conversation_id"`
	Direction      string    `json:"direction"`
	Content        string    `json:"content"`
	Sender         string    `json:"sender"`
	ExternalID     string    `json:"external_id,omitempty"`
	ReplyToID      *int64    `json:"reply_to_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type ConversationDetails struct {
	Conversation Conversation `json:"conversation"`
	Messages     []Message    `json:"messages"`
}

type SimulationResult struct {
	Conversation Conversation `json:"conversation"`
	Incoming     Message      `json:"incoming_message"`
	Reply        *Message     `json:"reply,omitempty"`
	Duplicate    bool         `json:"duplicate,omitempty"`
}
