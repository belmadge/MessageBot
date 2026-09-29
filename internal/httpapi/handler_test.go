package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/portfolio/whatsapp-support/internal/ai"
	"github.com/portfolio/whatsapp-support/internal/service"
)

type applicationStub struct {
	conversation service.Conversation
	err          error
	simulateErr  error
	calledWith   int64
}

func (a *applicationStub) SimulateInbound(context.Context, string, string, string) (service.SimulationResult, error) {
	return service.SimulationResult{}, a.simulateErr
}

func (a *applicationStub) ListConversations(context.Context, service.Status) ([]service.Conversation, error) {
	return nil, nil
}

func (a *applicationStub) GetConversation(context.Context, int64) (service.ConversationDetails, error) {
	return service.ConversationDetails{}, nil
}

func (a *applicationStub) TakeoverConversation(_ context.Context, id int64) (service.Conversation, error) {
	a.calledWith = id
	return a.conversation, a.err
}

func (a *applicationStub) SendHumanMessage(context.Context, int64, string) (service.Message, error) {
	return service.Message{}, nil
}

func (a *applicationStub) ResolveConversation(context.Context, int64) (service.Conversation, error) {
	return service.Conversation{}, nil
}

func TestTakeoverEndpointReturnsConversation(t *testing.T) {
	app := &applicationStub{conversation: service.Conversation{ID: 42, Status: service.StatusHuman}}
	handler := New(app)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/42/takeover", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusOK)
	}
	if app.calledWith != 42 {
		t.Fatalf("takeover called with ID %d, want 42", app.calledWith)
	}
	var result service.Conversation
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.ID != 42 || result.Status != service.StatusHuman {
		t.Fatalf("response = %#v, want conversation 42 in human status", result)
	}
}

func TestTakeoverEndpointReturnsConflictForResolvedConversation(t *testing.T) {
	app := &applicationStub{err: service.ErrConversationResolved}
	handler := New(app)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/42/takeover", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusConflict)
	}
}

func TestSimulatedInboundReturnsRetryableStatusWhileEventIsProcessing(t *testing.T) {
	app := &applicationStub{simulateErr: service.ErrInboundProcessing}
	handler := New(app)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/simulated/whatsapp/messages", strings.NewReader(`{"phone":"5511999999999","content":"oi","external_id":"event-in-progress"}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(recorder.Body.String(), "external_id") || strings.Contains(recorder.Body.String(), "provider") {
		t.Fatalf("response should be generic: %q", recorder.Body.String())
	}
}

func TestSimulatedInboundMapsProviderFailuresWithoutLeakingDetails(t *testing.T) {
	tests := []struct {
		name         string
		kind         service.AIProviderFailureKind
		providerKind ai.ProviderErrorKind
		statusCode   int
		wantHTTP     int
	}{
		{name: "invalid request", kind: service.AIProviderFailureInvalidRequest, providerKind: ai.ProviderErrorInvalidRequest, statusCode: http.StatusBadRequest, wantHTTP: http.StatusBadGateway},
		{name: "authentication", kind: service.AIProviderFailureAuthentication, providerKind: ai.ProviderErrorAuthentication, statusCode: http.StatusForbidden, wantHTTP: http.StatusBadGateway},
		{name: "provider quota", kind: service.AIProviderFailureRateLimited, providerKind: ai.ProviderErrorRateLimited, statusCode: http.StatusTooManyRequests, wantHTTP: http.StatusServiceUnavailable},
		{name: "upstream", kind: service.AIProviderFailureUpstream, providerKind: ai.ProviderErrorUpstream, statusCode: http.StatusInternalServerError, wantHTTP: http.StatusBadGateway},
		{name: "transport", kind: service.AIProviderFailureTransport, providerKind: ai.ProviderErrorTransport, statusCode: 0, wantHTTP: http.StatusBadGateway},
		{name: "invalid response", kind: service.AIProviderFailureInvalidResponse, providerKind: ai.ProviderErrorInvalidResponse, statusCode: http.StatusOK, wantHTTP: http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providerErr := &ai.ProviderError{
				Kind:       tt.providerKind,
				StatusCode: tt.statusCode,
				Code:       "credit_balance_exhausted",
				Message:    "quota exhausted",
				Err:        errors.New("full upstream body contains private-test-key and must-not-be-logged"),
			}
			app := &applicationStub{simulateErr: &service.AIProviderFailure{Kind: tt.kind, Err: providerErr}}
			handler := New(app)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/simulated/whatsapp/messages", strings.NewReader(`{"phone":"5511999999999","content":"oi","external_id":"provider-error-event"}`))
			request.Header.Set("Content-Type", "application/json")

			var logs bytes.Buffer
			previousWriter := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(previousWriter) })
			handler.ServeHTTP(recorder, request)
			log.SetOutput(previousWriter)

			if recorder.Code != tt.wantHTTP {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantHTTP)
			}
			for _, secret := range []string{"private-test-key", "must-not-be-logged", "credit_balance_exhausted", "stack trace", "internal server"} {
				if strings.Contains(recorder.Body.String(), secret) {
					t.Errorf("HTTP response leaked %q: %q", secret, recorder.Body.String())
				}
			}
			if strings.Contains(logs.String(), "private-test-key") || strings.Contains(logs.String(), "must-not-be-logged") {
				t.Errorf("internal log retained the full cause/body: %q", logs.String())
			}
		})
	}
}

type contactsMemory struct{}

func (contactsMemory) FindOrCreateByPhone(_ context.Context, phone string) (service.Contact, error) {
	return service.Contact{ID: 1, Phone: phone}, nil
}

type conversationsMemory struct {
	items map[int64]service.Conversation
	next  int64
}

func (r *conversationsMemory) FindActiveByContact(_ context.Context, contactID int64) (service.Conversation, error) {
	for _, item := range r.items {
		if item.ContactID == contactID && item.Status != service.StatusResolved {
			return item, nil
		}
	}
	return service.Conversation{}, service.ErrNotFound
}

func (r *conversationsMemory) Create(_ context.Context, contactID int64) (service.Conversation, error) {
	r.next++
	item := service.Conversation{ID: r.next, ContactID: contactID, Status: service.StatusBot}
	r.items[item.ID] = item
	return item, nil
}

func (r *conversationsMemory) Get(_ context.Context, id int64) (service.Conversation, error) {
	item, ok := r.items[id]
	if !ok {
		return service.Conversation{}, service.ErrNotFound
	}
	return item, nil
}

func (r *conversationsMemory) List(_ context.Context, status service.Status) ([]service.Conversation, error) {
	var result []service.Conversation
	for _, item := range r.items {
		if status == "" || item.Status == status {
			result = append(result, item)
		}
	}
	return result, nil
}

func (r *conversationsMemory) Takeover(_ context.Context, id int64) (service.Conversation, error) {
	item, ok := r.items[id]
	if !ok || item.Status != service.StatusBot {
		return service.Conversation{}, service.ErrNotFound
	}
	item.Status = service.StatusHuman
	r.items[id] = item
	return item, nil
}

func (r *conversationsMemory) UpdateStatus(_ context.Context, id int64, status service.Status) (service.Conversation, error) {
	item, ok := r.items[id]
	if !ok {
		return service.Conversation{}, service.ErrNotFound
	}
	item.Status = status
	r.items[id] = item
	return item, nil
}

func (r *conversationsMemory) Resolve(_ context.Context, id int64) (service.Conversation, error) {
	item, ok := r.items[id]
	if !ok {
		return service.Conversation{}, service.ErrNotFound
	}
	item.Status = service.StatusResolved
	r.items[id] = item
	return item, nil
}

type messagesMemory struct {
	items   map[int64][]service.Message
	events  map[string]service.Message
	replies map[int64]service.Message
	claims  map[int64]time.Time
	next    int64
}

func (r *messagesMemory) Create(_ context.Context, conversationID int64, direction, content, sender string) (service.Message, error) {
	r.next++
	item := service.Message{ID: r.next, ConversationID: conversationID, Direction: direction, Content: content, Sender: sender}
	r.items[conversationID] = append(r.items[conversationID], item)
	return item, nil
}

func (r *messagesMemory) CreateHumanMessage(ctx context.Context, conversationID int64, content string) (service.Message, error) {
	return r.Create(ctx, conversationID, "outbound", content, "human")
}

func (r *messagesMemory) CreateInbound(ctx context.Context, conversationID int64, content, externalID string) (service.Message, bool, error) {
	if _, exists := r.events[externalID]; exists {
		return service.Message{}, false, nil
	}
	item, err := r.Create(ctx, conversationID, "inbound", content, "contact")
	if err != nil {
		return service.Message{}, false, err
	}
	item.ExternalID = externalID
	r.items[conversationID][len(r.items[conversationID])-1] = item
	r.events[externalID] = item
	return item, true, nil
}

func (r *messagesMemory) FindInboundByExternalID(_ context.Context, externalID string) (service.Message, error) {
	item, ok := r.events[externalID]
	if !ok {
		return service.Message{}, service.ErrNotFound
	}
	return item, nil
}

func (r *messagesMemory) CreateReply(ctx context.Context, conversationID, inboundID int64, content string) (service.Message, bool, error) {
	if _, exists := r.replies[inboundID]; exists {
		return service.Message{}, false, nil
	}
	item, err := r.Create(ctx, conversationID, "outbound", content, "ai")
	if err != nil {
		return service.Message{}, false, err
	}
	item.ReplyToID = &inboundID
	r.items[conversationID][len(r.items[conversationID])-1] = item
	r.replies[inboundID] = item
	return item, true, nil
}

func (r *messagesMemory) FindReplyByInboundID(_ context.Context, inboundID int64) (*service.Message, error) {
	item, ok := r.replies[inboundID]
	if !ok {
		return nil, nil
	}
	return &item, nil
}

func (r *messagesMemory) ListByConversation(_ context.Context, conversationID int64) ([]service.Message, error) {
	return r.items[conversationID], nil
}

func (r *messagesMemory) ClaimInboundProcessing(_ context.Context, inboundID int64) (time.Time, bool, error) {
	if _, replied := r.replies[inboundID]; replied {
		return time.Time{}, false, nil
	}
	if _, claimed := r.claims[inboundID]; claimed {
		return time.Time{}, false, nil
	}
	claimedAt := time.Now()
	r.claims[inboundID] = claimedAt
	return claimedAt, true, nil
}

func (r *messagesMemory) ReleaseInboundProcessing(_ context.Context, inboundID int64, claimedAt time.Time) error {
	if r.claims[inboundID] == claimedAt {
		delete(r.claims, inboundID)
	}
	return nil
}

type providerMemory struct{ calls int }

func (p *providerMemory) GenerateReply(_ context.Context, input ai.Input) (ai.AIReply, error) {
	p.calls++
	return ai.AIReply{Content: "resposta para " + input.Content}, nil
}

func newSimulationHandler() (http.Handler, *messagesMemory, *providerMemory) {
	conversations := &conversationsMemory{items: map[int64]service.Conversation{}}
	messages := &messagesMemory{
		items:   map[int64][]service.Message{},
		events:  map[string]service.Message{},
		replies: map[int64]service.Message{},
		claims:  map[int64]time.Time{},
	}
	provider := &providerMemory{}
	app := service.New(contactsMemory{}, conversations, messages, provider)
	return New(app), messages, provider
}

func postInbound(t *testing.T, handler http.Handler, payload string) (int, service.SimulationResult) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/simulated/whatsapp/messages", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var result service.SimulationResult
	if recorder.Code == http.StatusCreated || recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return recorder.Code, result
}

func TestSimulatedInboundHTTPCreatesFirstEvent(t *testing.T) {
	handler, messages, provider := newSimulationHandler()
	status, result := postInbound(t, handler, `{"phone":"5511999999999","content":"oi","external_id":"event-1"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want %d", status, http.StatusCreated)
	}
	if result.Incoming.ExternalID != "event-1" || result.Reply == nil || result.Duplicate {
		t.Fatalf("unexpected first result: %#v", result)
	}
	if len(messages.items[result.Conversation.ID]) != 2 || provider.calls != 1 {
		t.Fatalf("message count/provider calls = %d/%d, want 2/1", len(messages.items[result.Conversation.ID]), provider.calls)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/conversations/"+strconv.FormatInt(result.Conversation.ID, 10), nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var details service.ConversationDetails
	if err := json.Unmarshal(recorder.Body.Bytes(), &details); err != nil {
		t.Fatalf("decode conversation details: %v", err)
	}
	if len(details.Messages) != 2 || details.Messages[0].ExternalID != "event-1" {
		t.Fatalf("conversation messages = %#v, expected persisted external ID", details.Messages)
	}
}

func TestSimulatedInboundHTTPReturnsStoredResultForDuplicateEvent(t *testing.T) {
	handler, messages, provider := newSimulationHandler()
	payload := `{"phone":"5511999999999","content":"oi","external_id":"event-duplicate"}`
	_, first := postInbound(t, handler, payload)
	status, duplicate := postInbound(t, handler, payload)
	if status != http.StatusOK {
		t.Fatalf("duplicate status = %d, want %d", status, http.StatusOK)
	}
	if !duplicate.Duplicate || duplicate.Incoming.ID != first.Incoming.ID || duplicate.Reply == nil || duplicate.Reply.ID != first.Reply.ID {
		t.Fatalf("unexpected duplicate result: %#v", duplicate)
	}
	if len(messages.items[first.Conversation.ID]) != 2 || provider.calls != 1 {
		t.Fatalf("message count/provider calls = %d/%d, want 2/1", len(messages.items[first.Conversation.ID]), provider.calls)
	}
}

func TestSimulatedInboundHTTPProcessesDifferentEventIDs(t *testing.T) {
	handler, messages, provider := newSimulationHandler()
	_, first := postInbound(t, handler, `{"phone":"5511999999999","content":"oi","external_id":"event-a"}`)
	_, second := postInbound(t, handler, `{"phone":"5511999999999","content":"oi","external_id":"event-b"}`)
	if first.Incoming.ID == second.Incoming.ID || first.Incoming.ExternalID == second.Incoming.ExternalID {
		t.Fatal("different event IDs must create separate inbound messages")
	}
	if len(messages.items[first.Conversation.ID]) != 4 || provider.calls != 2 {
		t.Fatalf("message count/provider calls = %d/%d, want 4/2", len(messages.items[first.Conversation.ID]), provider.calls)
	}
}

func TestSimulatedInboundHTTPRejectsMissingExternalID(t *testing.T) {
	handler, messages, provider := newSimulationHandler()
	status, _ := postInbound(t, handler, `{"phone":"5511999999999","content":"oi"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	if len(messages.items) != 0 || provider.calls != 0 {
		t.Fatal("request without external_id must not be processed")
	}
}
