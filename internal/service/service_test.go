package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/portfolio/whatsapp-support/internal/ai"
)

type contactsFake struct{ contact Contact }

func (f *contactsFake) FindOrCreateByPhone(_ context.Context, phone string) (Contact, error) {
	f.contact.Phone = phone
	return f.contact, nil
}

type conversationsFake struct {
	items map[int64]Conversation
	next  int64
}

func (f *conversationsFake) FindActiveByContact(_ context.Context, contactID int64) (Conversation, error) {
	for _, item := range f.items {
		if item.ContactID == contactID && item.Status != StatusResolved {
			return item, nil
		}
	}
	return Conversation{}, ErrNotFound
}

func (f *conversationsFake) Create(_ context.Context, contactID int64) (Conversation, error) {
	f.next++
	item := Conversation{ID: f.next, ContactID: contactID, Status: StatusBot}
	f.items[item.ID] = item
	return item, nil
}

func (f *conversationsFake) Get(_ context.Context, id int64) (Conversation, error) {
	item, ok := f.items[id]
	if !ok {
		return Conversation{}, ErrNotFound
	}
	return item, nil
}

func (f *conversationsFake) List(_ context.Context, status Status) ([]Conversation, error) {
	items := []Conversation{}
	for _, item := range f.items {
		if status == "" || item.Status == status {
			items = append(items, item)
		}
	}
	return items, nil
}

func (f *conversationsFake) Takeover(_ context.Context, id int64) (Conversation, error) {
	item, ok := f.items[id]
	if !ok || item.Status != StatusBot {
		return Conversation{}, ErrNotFound
	}
	item.Status = StatusHuman
	f.items[id] = item
	return item, nil
}

func (f *conversationsFake) UpdateStatus(_ context.Context, id int64, status Status) (Conversation, error) {
	item, ok := f.items[id]
	if !ok {
		return Conversation{}, ErrNotFound
	}
	item.Status = status
	f.items[id] = item
	return item, nil
}

func (f *conversationsFake) Resolve(_ context.Context, id int64) (Conversation, error) {
	item, ok := f.items[id]
	if !ok {
		return Conversation{}, ErrNotFound
	}
	item.Status = StatusResolved
	f.items[id] = item
	return item, nil
}

type messagesFake struct {
	items              map[int64][]Message
	events             map[string]Message
	replies            map[int64]Message
	claims             map[int64]time.Time
	claimMu            sync.Mutex
	next               int64
	canReply           func(int64) bool
	conversationStatus func(int64) (Status, bool)
	setHumanStatus     func(int64)
	humanMessageErr    error
}

func (f *messagesFake) Create(_ context.Context, conversationID int64, direction, content, sender string) (Message, error) {
	f.next++
	item := Message{ID: f.next, ConversationID: conversationID, Direction: direction, Content: content, Sender: sender}
	f.items[conversationID] = append(f.items[conversationID], item)
	return item, nil
}

func (f *messagesFake) CreateHumanMessage(ctx context.Context, conversationID int64, content string) (Message, error) {
	if f.humanMessageErr != nil {
		return Message{}, f.humanMessageErr
	}
	status, exists := f.conversationStatus(conversationID)
	if !exists {
		return Message{}, ErrNotFound
	}
	if status == StatusResolved {
		return Message{}, ErrConversationResolved
	}
	if status != StatusBot && status != StatusHuman {
		return Message{}, ErrNotFound
	}
	message, err := f.Create(ctx, conversationID, "outbound", content, "human")
	if err != nil {
		return Message{}, err
	}
	f.setHumanStatus(conversationID)
	return message, nil
}

func (f *messagesFake) CreateInbound(ctx context.Context, conversationID int64, content, externalID string) (Message, bool, error) {
	if _, exists := f.events[externalID]; exists {
		return Message{}, false, nil
	}
	message, err := f.Create(ctx, conversationID, "inbound", content, "contact")
	if err != nil {
		return Message{}, false, err
	}
	message.ExternalID = externalID
	f.items[conversationID][len(f.items[conversationID])-1] = message
	f.events[externalID] = message
	return message, true, nil
}

func (f *messagesFake) FindInboundByExternalID(_ context.Context, externalID string) (Message, error) {
	message, ok := f.events[externalID]
	if !ok {
		return Message{}, ErrNotFound
	}
	return message, nil
}

func (f *messagesFake) CreateReply(ctx context.Context, conversationID, inboundMessageID int64, content string) (Message, bool, error) {
	if f.canReply != nil && !f.canReply(conversationID) {
		return Message{}, false, nil
	}
	if _, exists := f.replies[inboundMessageID]; exists {
		return Message{}, false, nil
	}
	message, err := f.Create(ctx, conversationID, "outbound", content, "ai")
	if err != nil {
		return Message{}, false, err
	}
	message.ReplyToID = &inboundMessageID
	f.items[conversationID][len(f.items[conversationID])-1] = message
	f.replies[inboundMessageID] = message
	return message, true, nil
}

func (f *messagesFake) FindReplyByInboundID(_ context.Context, inboundMessageID int64) (*Message, error) {
	message, ok := f.replies[inboundMessageID]
	if !ok {
		return nil, nil
	}
	return &message, nil
}

func (f *messagesFake) ListByConversation(_ context.Context, id int64) ([]Message, error) {
	return f.items[id], nil
}

func (f *messagesFake) ClaimInboundProcessing(_ context.Context, id int64) (time.Time, bool, error) {
	f.claimMu.Lock()
	defer f.claimMu.Unlock()
	if _, replied := f.replies[id]; replied {
		return time.Time{}, false, nil
	}
	if claimedAt, claimed := f.claims[id]; claimed && time.Since(claimedAt) < time.Minute {
		return time.Time{}, false, nil
	}
	claimedAt := time.Now()
	f.claims[id] = claimedAt
	return claimedAt, true, nil
}

func (f *messagesFake) ReleaseInboundProcessing(_ context.Context, id int64, claimedAt time.Time) error {
	f.claimMu.Lock()
	defer f.claimMu.Unlock()
	if f.claims[id] == claimedAt {
		delete(f.claims, id)
	}
	return nil
}

type providerFake struct {
	reply      ai.AIReply
	err        error
	calls      int
	input      ai.Input
	onGenerate func()
}

func (f *providerFake) GenerateReply(_ context.Context, input ai.Input) (ai.AIReply, error) {
	f.calls++
	f.input = input
	if f.onGenerate != nil {
		f.onGenerate()
	}
	return f.reply, f.err
}

func newTestService(reply ai.AIReply) (*Service, *conversationsFake, *messagesFake, *providerFake) {
	contacts := &contactsFake{contact: Contact{ID: 1}}
	conversations := &conversationsFake{items: map[int64]Conversation{}}
	messages := &messagesFake{
		items:   map[int64][]Message{},
		events:  map[string]Message{},
		replies: map[int64]Message{},
		claims:  map[int64]time.Time{},
	}
	provider := &providerFake{reply: reply}
	messages.canReply = func(id int64) bool {
		conversation, ok := conversations.items[id]
		return ok && conversation.Status == StatusBot
	}
	messages.conversationStatus = func(id int64) (Status, bool) {
		conversation, ok := conversations.items[id]
		return conversation.Status, ok
	}
	messages.setHumanStatus = func(id int64) {
		conversation := conversations.items[id]
		conversation.Status = StatusHuman
		conversations.items[id] = conversation
	}
	return New(contacts, conversations, messages, provider), conversations, messages, provider
}

func TestSimulateInboundStoresIncomingAndAIReply(t *testing.T) {
	s, _, messages, provider := newTestService(ai.AIReply{Content: "resposta"})
	result, err := s.SimulateInbound(context.Background(), " +5511999999999 ", "  dúvida  ", "evt-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Incoming.Content != "dúvida" || result.Reply == nil || result.Reply.Content != "resposta" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(messages.items[result.Conversation.ID]) != 2 || provider.calls != 1 {
		t.Fatalf("messages/provider calls = %d/%d", len(messages.items[result.Conversation.ID]), provider.calls)
	}
}

func TestSimulateInboundPassesOnlyEarlierMessagesFromSameConversation(t *testing.T) {
	s, _, messages, provider := newTestService(ai.AIReply{Content: "resposta"})
	first, err := s.SimulateInbound(context.Background(), "5511999999999", "Olá 😊", "evt-context-first")
	if err != nil {
		t.Fatal(err)
	}
	// A repository bug must not leak a message associated with another conversation.
	messages.items[first.Conversation.ID] = append(messages.items[first.Conversation.ID], Message{
		ID: messages.next + 100, ConversationID: first.Conversation.ID + 1,
		Direction: "inbound", Content: "segredo de outra conversa",
	})
	second, err := s.SimulateInbound(context.Background(), "5511999999999", "ação atual", "evt-context-second")
	if err != nil {
		t.Fatal(err)
	}
	if second.Incoming.Content != "ação atual" || provider.input.Content != "ação atual" {
		t.Fatalf("current message = %q / input content = %q", second.Incoming.Content, provider.input.Content)
	}
	if len(provider.input.History) != 2 {
		t.Fatalf("history length = %d, want 2: %+v", len(provider.input.History), provider.input.History)
	}
	want := []ai.Message{{Role: "user", Content: "Olá 😊"}, {Role: "assistant", Content: "resposta"}}
	for i := range want {
		if provider.input.History[i] != want[i] {
			t.Fatalf("history[%d] = %+v, want %+v", i, provider.input.History[i], want[i])
		}
	}
	for _, message := range provider.input.History {
		if message.Content == second.Incoming.Content || message.Content == "segredo de outra conversa" {
			t.Fatalf("current or cross-conversation message leaked into history: %+v", message)
		}
	}
}

func TestBuildAIHistoryLimitsMessagesCharactersAndKeepsWholeMessages(t *testing.T) {
	conversationID := int64(7)
	messages := make([]Message, 0, 12)
	for i := int64(1); i <= 11; i++ {
		messages = append(messages, Message{ID: i, ConversationID: conversationID, Direction: "inbound", Content: strconv.FormatInt(i, 10)})
	}
	got := buildAIHistory(append(messages, Message{ID: 12, ConversationID: conversationID, Direction: "inbound", Content: "current"}), conversationID, 12)
	if len(got) != maxAIHistoryMessages {
		t.Fatalf("history length = %d, want %d", len(got), maxAIHistoryMessages)
	}
	if got[0].Content != "2" || got[len(got)-1].Content != "11" {
		t.Fatalf("history is not the latest chronological slice: %+v", got)
	}

	messages = []Message{{ID: 1, ConversationID: conversationID, Direction: "inbound", Content: strings.Repeat("a", 3001)},
		{ID: 2, ConversationID: conversationID, Direction: "outbound", Content: strings.Repeat("b", 3000)},
		{ID: 3, ConversationID: conversationID, Direction: "inbound", Content: "current"}}
	got = buildAIHistory(messages, conversationID, 3)
	if len(got) != 1 || got[0].Content != strings.Repeat("b", 3000) {
		t.Fatalf("history should retain the latest whole message fitting the budget: count=%d", len(got))
	}
	if characters := utf8.RuneCountInString(got[0].Content); characters > maxAIHistoryCharacters {
		t.Fatalf("history has %d characters, exceeds %d", characters, maxAIHistoryCharacters)
	}

	messages = []Message{{ID: 1, ConversationID: conversationID, Direction: "inbound", Content: "z"},
		{ID: 2, ConversationID: conversationID, Direction: "inbound", Content: strings.Repeat("😊", 3000)},
		{ID: 3, ConversationID: conversationID, Direction: "outbound", Content: strings.Repeat("ã", 3000)},
		{ID: 4, ConversationID: conversationID, Direction: "inbound", Content: "atual"}}
	got = buildAIHistory(messages, conversationID, 4)
	if len(got) != 2 || got[0].Content != strings.Repeat("😊", 3000) || got[1].Content != strings.Repeat("ã", 3000) {
		t.Fatalf("Unicode history should preserve the latest whole messages in order: count=%d", len(got))
	}
	if characters := utf8.RuneCountInString(got[0].Content) + utf8.RuneCountInString(got[1].Content); characters != maxAIHistoryCharacters {
		t.Fatalf("Unicode history has %d characters, want exact limit %d", characters, maxAIHistoryCharacters)
	}
}

func TestSimulateInboundPreservesProviderFailureCause(t *testing.T) {
	s, _, _, provider := newTestService(ai.AIReply{})
	cause := errors.New("underlying provider diagnostic")
	provider.err = &ai.ProviderError{
		Kind:       ai.ProviderErrorRateLimited,
		StatusCode: 429,
		Code:       "credit_balance_exhausted",
		Message:    "quota exhausted",
		Err:        cause,
	}

	_, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-provider-error")
	var failure *AIProviderFailure
	if !errors.As(err, &failure) || failure.Kind != AIProviderFailureRateLimited {
		t.Fatalf("error = %v, want rate-limited AIProviderFailure", err)
	}
	var providerErr *ai.ProviderError
	if !errors.As(err, &providerErr) || providerErr.StatusCode != 429 {
		t.Fatalf("error chain does not contain original provider error: %v", err)
	}
	if !errors.Is(err, ai.ErrProviderRateLimited) || !errors.Is(err, cause) {
		t.Fatalf("error chain did not preserve provider classification and cause: %v", err)
	}
}

func TestExplicitHumanRequestSkipsProviderAndRoutesConversation(t *testing.T) {
	s, conversations, messages, provider := newTestService(ai.AIReply{Content: "not used"})
	result, err := s.SimulateInbound(context.Background(), "5511999999999", "Quero falar com um atendente", "evt-human")
	if err != nil {
		t.Fatal(err)
	}
	if result.Conversation.Status != StatusHuman || result.Reply != nil {
		t.Fatalf("unexpected result: %#v", result)
	}
	if provider.calls != 0 || len(messages.items[result.Conversation.ID]) != 1 {
		t.Fatal("provider should be skipped and only inbound message stored")
	}
	if conversations.items[result.Conversation.ID].Status != StatusHuman {
		t.Fatal("conversation was not handed off")
	}
}

func TestProviderCanRequestHuman(t *testing.T) {
	s, _, _, _ := newTestService(ai.AIReply{RequiresHuman: true})
	result, err := s.SimulateInbound(context.Background(), "5511999999999", "preciso de ajuda", "evt-ai-human")
	if err != nil {
		t.Fatal(err)
	}
	if result.Conversation.Status != StatusHuman || result.Reply != nil {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestAIReplyIsNotPersistedIfTakeoverHappensDuringProviderCall(t *testing.T) {
	s, conversations, messages, provider := newTestService(ai.AIReply{Content: "late reply"})
	started, release := make(chan struct{}), make(chan struct{})
	provider.onGenerate = func() { close(started); <-release }
	type outcome struct {
		result SimulationResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-takeover-race")
		done <- outcome{result, err}
	}()
	<-started
	if _, err := s.TakeoverConversation(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.result.Reply != nil || got.result.Conversation.Status != StatusHuman || len(messages.items[1]) != 1 {
		t.Fatalf("late AI result persisted or changed status: result=%#v messages=%d", got.result, len(messages.items[1]))
	}
	if conversations.items[1].Status != StatusHuman {
		t.Fatalf("status = %s, want human", conversations.items[1].Status)
	}
}

func TestAIReplyIsNotPersistedIfResolveHappensDuringProviderCall(t *testing.T) {
	s, conversations, messages, provider := newTestService(ai.AIReply{Content: "late reply"})
	started, release := make(chan struct{}), make(chan struct{})
	provider.onGenerate = func() { close(started); <-release }
	type outcome struct {
		result SimulationResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-resolve-race")
		done <- outcome{result, err}
	}()
	<-started
	if _, err := s.ResolveConversation(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.result.Reply != nil || got.result.Conversation.Status != StatusResolved || len(messages.items[1]) != 1 {
		t.Fatalf("late AI result persisted or changed status: result=%#v messages=%d", got.result, len(messages.items[1]))
	}
	if conversations.items[1].Status != StatusResolved {
		t.Fatalf("status = %s, want resolved", conversations.items[1].Status)
	}
}

func TestRequiresHumanDoesNotReopenConversationResolvedDuringProviderCall(t *testing.T) {
	s, conversations, messages, provider := newTestService(ai.AIReply{RequiresHuman: true})
	started, release := make(chan struct{}), make(chan struct{})
	provider.onGenerate = func() { close(started); <-release }
	type outcome struct {
		result SimulationResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-route-race")
		done <- outcome{result, err}
	}()
	<-started
	if _, err := s.ResolveConversation(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	close(release)
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.result.Conversation.Status != StatusResolved || got.result.Reply != nil || len(messages.items[1]) != 1 {
		t.Fatalf("handoff reopened/persisted after resolve: result=%#v messages=%d", got.result, len(messages.items[1]))
	}
	if conversations.items[1].Status != StatusResolved {
		t.Fatalf("status = %s, want resolved", conversations.items[1].Status)
	}
}

func TestDuplicateInboundReturnsOriginalResultWithoutCallingProviderAgain(t *testing.T) {
	s, _, messages, provider := newTestService(ai.AIReply{Content: "resposta original"})
	first, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-duplicate")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-duplicate")
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Incoming.ID != first.Incoming.ID || duplicate.Reply == nil || duplicate.Reply.ID != first.Reply.ID {
		t.Fatalf("duplicate result = %#v, want original inbound and reply", duplicate)
	}
	if len(messages.items[first.Conversation.ID]) != 2 || provider.calls != 1 {
		t.Fatalf("message count/provider calls = %d/%d, want 2/1", len(messages.items[first.Conversation.ID]), provider.calls)
	}
}

func TestIncompleteInboundCanRetryAfterProviderFailure(t *testing.T) {
	s, _, messages, provider := newTestService(ai.AIReply{Content: "resposta recuperada"})
	provider.err = errors.New("temporary provider failure")
	first, err := s.SimulateInbound(context.Background(), "5511999999999", "olá 😊", "evt-retry")
	if err == nil {
		t.Fatal("first attempt error = nil, want provider error")
	}
	if len(messages.items[first.Conversation.ID]) != 1 || len(messages.replies) != 0 {
		t.Fatalf("failed attempt should leave one inbound and no reply: messages=%d replies=%d", len(messages.items[first.Conversation.ID]), len(messages.replies))
	}
	if len(messages.claims) != 0 {
		t.Fatal("provider failure should release the processing claim")
	}

	provider.err = nil
	second, err := s.SimulateInbound(context.Background(), "5511999999999", "olá 😊", "evt-retry")
	if err != nil {
		t.Fatal(err)
	}
	if second.Incoming.ID != first.Incoming.ID || second.Incoming.Content != "olá 😊" || second.Reply == nil || second.Reply.Content != "resposta recuperada" {
		t.Fatalf("retry result = %#v, want same Unicode inbound with reply", second)
	}
	if provider.calls != 2 || len(messages.items[first.Conversation.ID]) != 2 || len(messages.replies) != 1 {
		t.Fatalf("calls/messages/replies = %d/%d/%d, want 2/2/1", provider.calls, len(messages.items[first.Conversation.ID]), len(messages.replies))
	}
}

func TestRepeatedProviderFailureLeavesInboundRetryable(t *testing.T) {
	s, _, messages, provider := newTestService(ai.AIReply{})
	provider.err = errors.New("provider unavailable")
	for attempt := 0; attempt < 2; attempt++ {
		_, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-retry-fails")
		if err == nil {
			t.Fatalf("attempt %d error = nil, want provider failure", attempt+1)
		}
	}
	if provider.calls != 2 || len(messages.items[1]) != 1 || len(messages.replies) != 0 || len(messages.claims) != 0 {
		t.Fatalf("calls/inbound/replies/claims = %d/%d/%d/%d; want 2/1/0/0", provider.calls, len(messages.items[1]), len(messages.replies), len(messages.claims))
	}
}

func TestConcurrentDuplicateDoesNotCreateDuplicateReply(t *testing.T) {
	s, _, messages, provider := newTestService(ai.AIReply{Content: "uma resposta"})
	started := make(chan struct{})
	release := make(chan struct{})
	provider.onGenerate = func() {
		close(started)
		<-release
	}

	type result struct {
		value SimulationResult
		err   error
	}
	firstDone := make(chan result, 1)
	go func() {
		value, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-concurrent")
		firstDone <- result{value: value, err: err}
	}()
	<-started

	second, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-concurrent")
	if !errors.Is(err, ErrInboundProcessing) {
		t.Fatalf("concurrent duplicate error = %v, want ErrInboundProcessing", err)
	}
	if !second.Duplicate || second.Reply != nil || provider.calls != 1 {
		t.Fatalf("concurrent result = %#v, provider calls = %d; want in-progress duplicate and one call", second, provider.calls)
	}
	close(release)
	first := <-firstDone
	if first.err != nil {
		t.Fatal(first.err)
	}
	if first.value.Reply == nil || first.value.Incoming.ID != second.Incoming.ID {
		t.Fatalf("first result = %#v; expected reply for shared inbound", first.value)
	}
	if len(messages.items[first.value.Conversation.ID]) != 2 || len(messages.replies) != 1 {
		t.Fatalf("messages/replies = %d/%d, want 2/1", len(messages.items[first.value.Conversation.ID]), len(messages.replies))
	}
	provider.onGenerate = nil
	third, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-concurrent")
	if err != nil || third.Reply == nil || third.Reply.ID != first.value.Reply.ID || provider.calls != 1 {
		t.Fatalf("completed duplicate = %#v, err=%v, provider calls=%d", third, err, provider.calls)
	}
}

func TestDifferentExternalIDsAreProcessedAsSeparateMessages(t *testing.T) {
	s, _, messages, provider := newTestService(ai.AIReply{Content: "resposta"})
	first, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-two")
	if err != nil {
		t.Fatal(err)
	}
	if first.Incoming.ID == second.Incoming.ID {
		t.Fatal("different external IDs must create different inbound messages")
	}
	if len(messages.items[first.Conversation.ID]) != 4 || provider.calls != 2 {
		t.Fatalf("message count/provider calls = %d/%d, want 4/2", len(messages.items[first.Conversation.ID]), provider.calls)
	}
}

func TestRejectsInboundWithoutExternalID(t *testing.T) {
	s, _, messages, provider := newTestService(ai.AIReply{Content: "resposta"})
	_, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "  ")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidInput)
	}
	if len(messages.items) != 0 || provider.calls != 0 {
		t.Fatal("invalid inbound must not be persisted or sent to the provider")
	}
}

func TestTakeoverMovesBotConversationToHumanWithoutSendingMessage(t *testing.T) {
	s, conversations, messages, provider := newTestService(ai.AIReply{Content: "unused"})
	conversation, err := conversations.Create(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	result, err := s.TakeoverConversation(context.Background(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusHuman {
		t.Fatalf("status = %q, want %q", result.Status, StatusHuman)
	}
	if len(messages.items[conversation.ID]) != 0 || provider.calls != 0 {
		t.Fatal("takeover must not create a message or call the AI provider")
	}
}

func TestTakeoverIsIdempotentForHumanConversation(t *testing.T) {
	s, conversations, messages, _ := newTestService(ai.AIReply{})
	conversation, err := conversations.Create(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversations.UpdateStatus(context.Background(), conversation.ID, StatusHuman); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		result, err := s.TakeoverConversation(context.Background(), conversation.ID)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != StatusHuman {
			t.Fatalf("status = %q, want %q", result.Status, StatusHuman)
		}
	}
	if len(messages.items[conversation.ID]) != 0 {
		t.Fatal("takeover must not create a message")
	}
}

func TestTakeoverDoesNotReopenResolvedConversation(t *testing.T) {
	s, conversations, _, _ := newTestService(ai.AIReply{})
	conversation, err := conversations.Create(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversations.UpdateStatus(context.Background(), conversation.ID, StatusResolved); err != nil {
		t.Fatal(err)
	}

	_, err = s.TakeoverConversation(context.Background(), conversation.ID)
	if !errors.Is(err, ErrConversationResolved) {
		t.Fatalf("error = %v, want %v", err, ErrConversationResolved)
	}
	current, err := conversations.Get(context.Background(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != StatusResolved {
		t.Fatalf("status = %q, want %q", current.Status, StatusResolved)
	}
}

func TestHumanConversationDoesNotCallProvider(t *testing.T) {
	s, conversations, _, provider := newTestService(ai.AIReply{Content: "resposta"})
	first, err := s.SimulateInbound(context.Background(), "5511999999999", "primeira", "evt-human-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversations.UpdateStatus(context.Background(), first.Conversation.ID, StatusHuman); err != nil {
		t.Fatal(err)
	}
	result, err := s.SimulateInbound(context.Background(), "5511999999999", "segunda", "evt-human-2")
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != nil || provider.calls != 1 {
		t.Fatal("human-owned conversation should not invoke provider")
	}
}

func TestRejectsEmptyInbound(t *testing.T) {
	s, _, _, _ := newTestService(ai.AIReply{Content: "resposta"})
	_, err := s.SimulateInbound(context.Background(), "5511999999999", "  ", "evt-empty")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
}

func TestCannotSendHumanReplyToResolvedConversation(t *testing.T) {
	s, _, _, _ := newTestService(ai.AIReply{Content: "resposta"})
	result, err := s.SimulateInbound(context.Background(), "5511999999999", "oi", "evt-resolve")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveConversation(context.Background(), result.Conversation.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.SendHumanMessage(context.Background(), result.Conversation.ID, "resposta humana")
	if !errors.Is(err, ErrConversationResolved) {
		t.Fatalf("expected ErrConversationResolved, got %v", err)
	}
}

func TestSendHumanMessageInHumanConversation(t *testing.T) {
	s, conversations, messages, _ := newTestService(ai.AIReply{})
	conversation, err := conversations.Create(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conversations.UpdateStatus(context.Background(), conversation.ID, StatusHuman); err != nil {
		t.Fatal(err)
	}

	message, err := s.SendHumanMessage(context.Background(), conversation.ID, "  resposta humana  ")
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "resposta humana" || message.Sender != "human" || len(messages.items[conversation.ID]) != 1 {
		t.Fatalf("unexpected human message: %#v, stored=%#v", message, messages.items[conversation.ID])
	}
}

func TestSendHumanMessageInBotConversationHandsOffAndSends(t *testing.T) {
	s, conversations, messages, _ := newTestService(ai.AIReply{})
	conversation, err := conversations.Create(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	message, err := s.SendHumanMessage(context.Background(), conversation.ID, "resposta humana")
	if err != nil {
		t.Fatal(err)
	}
	current, err := conversations.Get(context.Background(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != StatusHuman || message.Sender != "human" || len(messages.items[conversation.ID]) != 1 {
		t.Fatalf("bot handoff/message = status %s, message %#v, stored=%#v", current.Status, message, messages.items[conversation.ID])
	}
}

func TestHumanMessagePersistenceFailureDoesNotTransitionConversation(t *testing.T) {
	s, conversations, messages, _ := newTestService(ai.AIReply{})
	conversation, err := conversations.Create(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	persistenceErr := errors.New("message transaction failed")
	messages.humanMessageErr = persistenceErr

	if _, err := s.SendHumanMessage(context.Background(), conversation.ID, "resposta humana"); !errors.Is(err, persistenceErr) {
		t.Fatalf("SendHumanMessage() error = %v, want persistence error", err)
	}
	current, err := conversations.Get(context.Background(), conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != StatusBot || len(messages.items[conversation.ID]) != 0 {
		t.Fatalf("failed persistence left partial state: status=%s messages=%d", current.Status, len(messages.items[conversation.ID]))
	}
}
