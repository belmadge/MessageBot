package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/portfolio/whatsapp-support/internal/ai"
)

const (
	maxAIHistoryMessages   = 10
	maxAIHistoryCharacters = 6000
)

var (
	ErrNotFound             = errors.New("not found")
	ErrInvalidInput         = errors.New("invalid input")
	ErrConversationResolved = errors.New("conversation is already resolved")
	ErrInboundProcessing    = errors.New("inbound message is already being processed")
)

type ContactRepository interface {
	FindOrCreateByPhone(context.Context, string) (Contact, error)
}

type ConversationRepository interface {
	FindActiveByContact(context.Context, int64) (Conversation, error)
	Create(context.Context, int64) (Conversation, error)
	Get(context.Context, int64) (Conversation, error)
	List(context.Context, Status) ([]Conversation, error)
	Takeover(context.Context, int64) (Conversation, error)
	Resolve(context.Context, int64) (Conversation, error)
}

type MessageRepository interface {
	Create(context.Context, int64, string, string, string) (Message, error)
	CreateHumanMessage(context.Context, int64, string) (Message, error)
	CreateInbound(context.Context, int64, string, string) (Message, bool, error)
	FindInboundByExternalID(context.Context, string) (Message, error)
	CreateReply(context.Context, int64, int64, string) (Message, bool, error)
	FindReplyByInboundID(context.Context, int64) (*Message, error)
	ListByConversation(context.Context, int64) ([]Message, error)
	ClaimInboundProcessing(context.Context, int64) (time.Time, bool, error)
	ReleaseInboundProcessing(context.Context, int64, time.Time) error
}

type Service struct {
	contacts      ContactRepository
	conversations ConversationRepository
	messages      MessageRepository
	provider      ai.Provider
}

func New(contacts ContactRepository, conversations ConversationRepository, messages MessageRepository, provider ai.Provider) *Service {
	return &Service{
		contacts:      contacts,
		conversations: conversations,
		messages:      messages,
		provider:      provider,
	}
}

func (s *Service) SimulateInbound(ctx context.Context, phone, content, externalID string) (SimulationResult, error) {
	phone = strings.TrimSpace(phone)
	content = strings.TrimSpace(content)
	externalID = strings.TrimSpace(externalID)
	if phone == "" || content == "" || externalID == "" {
		return SimulationResult{}, ErrInvalidInput
	}
	priorResult, found, err := s.findProcessedEvent(ctx, externalID)
	if err != nil {
		return priorResult, err
	}
	if found {
		priorResult.Duplicate = true
		if priorResult.Reply != nil || priorResult.Conversation.Status != StatusBot {
			return priorResult, nil
		}
		if requestsHuman(priorResult.Incoming.Content) {
			priorResult.Conversation, err = s.routeToHuman(ctx, priorResult.Conversation.ID)
			return priorResult, err
		}
		return s.processIncompleteInbound(ctx, priorResult)
	}

	contact, err := s.contacts.FindOrCreateByPhone(ctx, phone)
	if err != nil {
		return SimulationResult{}, err
	}

	conversation, err := s.conversations.FindActiveByContact(ctx, contact.ID)
	if errors.Is(err, ErrNotFound) {
		conversation, err = s.conversations.Create(ctx, contact.ID)
	}
	if err != nil {
		return SimulationResult{}, err
	}

	incoming, created, err := s.messages.CreateInbound(ctx, conversation.ID, content, externalID)
	if err != nil {
		return SimulationResult{}, err
	}
	if !created {
		result, found, err := s.findProcessedEvent(ctx, externalID)
		result.Duplicate = found
		if err == nil && found && result.Reply == nil && result.Conversation.Status == StatusBot && !requestsHuman(result.Incoming.Content) {
			return s.processIncompleteInbound(ctx, result)
		}
		return result, err
	}
	result := SimulationResult{Conversation: conversation, Incoming: incoming}
	if conversation.Status != StatusBot {
		return result, nil
	}

	if requestsHuman(content) {
		result.Conversation, err = s.routeToHuman(ctx, conversation.ID)
		return result, err
	}

	return s.processIncompleteInbound(ctx, result)
}

func (s *Service) processIncompleteInbound(ctx context.Context, result SimulationResult) (SimulationResult, error) {
	claimAt, claimed, err := s.messages.ClaimInboundProcessing(ctx, result.Incoming.ID)
	if err != nil {
		return result, err
	}
	if !claimed {
		latest, found, err := s.findProcessedEvent(ctx, result.Incoming.ExternalID)
		if err != nil {
			return result, err
		}
		if found {
			latest.Duplicate = true
			if latest.Reply == nil && latest.Conversation.Status == StatusBot {
				return latest, ErrInboundProcessing
			}
			return latest, nil
		}
		return result, ErrInboundProcessing
	}

	conversationMessages, err := s.messages.ListByConversation(ctx, result.Conversation.ID)
	if err != nil {
		return result, s.releaseClaim(ctx, result.Incoming.ID, claimAt, err)
	}
	history := buildAIHistory(conversationMessages, result.Conversation.ID, result.Incoming.ID)
	reply, err := s.provider.GenerateReply(ctx, ai.Input{Content: result.Incoming.Content, History: history})
	if err != nil {
		return result, s.releaseClaim(ctx, result.Incoming.ID, claimAt, newAIProviderFailure(err))
	}
	if reply.RequiresHuman {
		result.Conversation, err = s.routeToHuman(ctx, result.Conversation.ID)
		if err != nil {
			return result, s.releaseClaim(ctx, result.Incoming.ID, claimAt, err)
		}
		return result, s.releaseClaim(ctx, result.Incoming.ID, claimAt, nil)
	}
	content := strings.TrimSpace(reply.Content)
	if content == "" {
		return result, s.releaseClaim(ctx, result.Incoming.ID, claimAt, errors.New("AI provider returned an empty reply"))
	}

	message, created, err := s.messages.CreateReply(ctx, result.Conversation.ID, result.Incoming.ID, content)
	if err != nil {
		return result, s.releaseClaim(ctx, result.Incoming.ID, claimAt, err)
	}
	if created {
		result.Reply = &message
	} else {
		result.Reply, err = s.messages.FindReplyByInboundID(ctx, result.Incoming.ID)
		if err != nil {
			return result, s.releaseClaim(ctx, result.Incoming.ID, claimAt, err)
		}
		result.Conversation, err = s.conversations.Get(ctx, result.Conversation.ID)
		if err != nil {
			return result, s.releaseClaim(ctx, result.Incoming.ID, claimAt, err)
		}
	}
	return result, s.releaseClaim(ctx, result.Incoming.ID, claimAt, nil)
}

// routeToHuman uses a conditional bot-to-human transition. A concurrent
// resolve wins permanently; automatic handoff then returns the resolved state.
func (s *Service) routeToHuman(ctx context.Context, id int64) (Conversation, error) {
	conversation, err := s.TakeoverConversation(ctx, id)
	if errors.Is(err, ErrConversationResolved) {
		return s.conversations.Get(ctx, id)
	}
	return conversation, err
}

func (s *Service) releaseClaim(ctx context.Context, inboundID int64, claimAt time.Time, cause error) error {
	if err := s.messages.ReleaseInboundProcessing(ctx, inboundID, claimAt); err != nil {
		return errors.Join(cause, errors.New("could not release inbound processing claim: "+err.Error()))
	}
	return cause
}

func buildAIHistory(messages []Message, conversationID, currentMessageID int64) []ai.Message {
	// Only consider persisted messages before the current inbound. If it is absent,
	// return no history rather than risk including the current or a later message.
	previous := make([]Message, 0, len(messages))
	foundCurrent := false
	for _, message := range messages {
		if message.ID == currentMessageID {
			foundCurrent = true
			break
		}
		if message.ConversationID == conversationID {
			previous = append(previous, message)
		}
	}
	if !foundCurrent {
		return nil
	}

	history := make([]ai.Message, 0, maxAIHistoryMessages)
	characters := 0
	for i := len(previous) - 1; i >= 0 && len(history) < maxAIHistoryMessages; i-- {
		message := previous[i]
		role := ""
		switch message.Direction {
		case "inbound":
			role = "user"
		case "outbound":
			role = "assistant"
		default:
			continue
		}
		contentCharacters := utf8.RuneCountInString(message.Content)
		if characters+contentCharacters > maxAIHistoryCharacters {
			break // Keep whole messages; never truncate content to fit the budget.
		}
		history = append(history, ai.Message{Role: role, Content: message.Content})
		characters += contentCharacters
	}
	slices.Reverse(history)
	return history
}

func (s *Service) findProcessedEvent(ctx context.Context, externalID string) (SimulationResult, bool, error) {
	incoming, err := s.messages.FindInboundByExternalID(ctx, externalID)
	if errors.Is(err, ErrNotFound) {
		return SimulationResult{}, false, nil
	}
	if err != nil {
		return SimulationResult{}, false, err
	}
	conversation, err := s.conversations.Get(ctx, incoming.ConversationID)
	if err != nil {
		return SimulationResult{}, false, err
	}
	reply, err := s.messages.FindReplyByInboundID(ctx, incoming.ID)
	if err != nil {
		return SimulationResult{}, false, err
	}
	return SimulationResult{Conversation: conversation, Incoming: incoming, Reply: reply}, true, nil
}

func (s *Service) ListConversations(ctx context.Context, status Status) ([]Conversation, error) {
	if status != "" && status != StatusBot && status != StatusHuman && status != StatusResolved {
		return nil, ErrInvalidInput
	}
	return s.conversations.List(ctx, status)
}

func (s *Service) GetConversation(ctx context.Context, id int64) (ConversationDetails, error) {
	conversation, err := s.conversations.Get(ctx, id)
	if err != nil {
		return ConversationDetails{}, err
	}
	messages, err := s.messages.ListByConversation(ctx, id)
	if err != nil {
		return ConversationDetails{}, err
	}
	return ConversationDetails{Conversation: conversation, Messages: messages}, nil
}

func (s *Service) TakeoverConversation(ctx context.Context, id int64) (Conversation, error) {
	conversation, err := s.conversations.Takeover(ctx, id)
	if err == nil {
		return conversation, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Conversation{}, err
	}

	conversation, err = s.conversations.Get(ctx, id)
	if err != nil {
		return Conversation{}, err
	}
	switch conversation.Status {
	case StatusHuman:
		return conversation, nil
	case StatusResolved:
		return Conversation{}, ErrConversationResolved
	default:
		return Conversation{}, errors.New("conversation could not be taken over")
	}
}

func (s *Service) SendHumanMessage(ctx context.Context, id int64, content string) (Message, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return Message{}, ErrInvalidInput
	}
	return s.messages.CreateHumanMessage(ctx, id, content)
}

func (s *Service) ResolveConversation(ctx context.Context, id int64) (Conversation, error) {
	return s.conversations.Resolve(ctx, id)
}

func requestsHuman(content string) bool {
	text := strings.ToLower(content)
	for _, term := range []string{"atendente", "humano", "pessoa", "falar com alguém", "falar com alguem"} {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}
