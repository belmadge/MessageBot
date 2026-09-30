package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/portfolio/whatsapp-support/internal/ai"
	"github.com/portfolio/whatsapp-support/internal/service"
)

func openConcurrencyTestDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect to test PostgreSQL: %v", err)
	}
	return db, ctx
}

func createConcurrencyFixture(t *testing.T, db *sql.DB, ctx context.Context) (service.Contact, service.Conversation) {
	t.Helper()
	suffix := time.Now().UnixNano()
	contact, err := (ContactRepository{DB: db}).FindOrCreateByPhone(ctx, fmt.Sprintf("concurrency-%d", suffix))
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := (ConversationRepository{DB: db}).Create(ctx, contact.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM conversations WHERE id = $1`, conversation.ID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM contacts WHERE id = $1`, contact.ID)
	})
	return contact, conversation
}

func TestPostgresConcurrentConversationCreateUsesUniqueActiveIndex(t *testing.T) {
	db, ctx := openConcurrencyTestDB(t)
	suffix := time.Now().UnixNano()
	contact, err := (ContactRepository{DB: db}).FindOrCreateByPhone(ctx, fmt.Sprintf("active-create-%d", suffix))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM conversations WHERE contact_id = $1`, contact.ID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM contacts WHERE id = $1`, contact.ID)
	})

	const workers = 16
	start := make(chan struct{})
	ids := make(chan int64, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			conversation, err := (ConversationRepository{DB: db}).Create(ctx, contact.ID)
			if err != nil {
				errs <- err
				return
			}
			ids <- conversation.ID
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Errorf("concurrent Create(): %v", err)
	}
	winner := int64(0)
	for id := range ids {
		if winner == 0 {
			winner = id
		}
		if id != winner {
			t.Fatalf("Create() returned multiple active conversations: %d and %d", winner, id)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM conversations WHERE contact_id = $1 AND status <> 'resolved'`, contact.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("active conversation count = %d, want 1", count)
	}
	// Verify the partial unique index rejects duplicates even when bypassing the repository.
	if _, err := db.ExecContext(ctx, `INSERT INTO conversations(contact_id) VALUES ($1)`, contact.ID); err == nil {
		t.Fatal("PostgreSQL accepted a second active conversation for the contact")
	}
	active, err := (ConversationRepository{DB: db}).FindActiveByContact(ctx, contact.ID)
	if err != nil || active.ID != winner {
		t.Fatalf("subsequent FindActiveByContact() = %#v, err %v; want winner %d", active, err, winner)
	}
}

func TestPostgresConcurrentMessagesForContactShareActiveConversation(t *testing.T) {
	db, ctx := openConcurrencyTestDB(t)
	phone := fmt.Sprintf("simultaneous-inbound-%d", time.Now().UnixNano())
	contacts, conversations, messages := ContactRepository{DB: db}, ConversationRepository{DB: db}, MessageRepository{DB: db}
	app := service.New(contacts, conversations, messages, ai.LocalProvider{})
	start := make(chan struct{})
	type result struct {
		value service.SimulationResult
		err   error
	}
	results := make(chan result, 2)
	for _, eventID := range []string{"first", "second"} {
		eventID := eventID
		go func() {
			<-start
			value, err := app.SimulateInbound(ctx, phone, "mensagem "+eventID, fmt.Sprintf("%s-%s", phone, eventID))
			results <- result{value, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil {
		t.Fatal(first.err)
	}
	if second.err != nil {
		t.Fatal(second.err)
	}
	if first.value.Conversation.ID != second.value.Conversation.ID {
		t.Fatalf("concurrent messages used conversations %d and %d", first.value.Conversation.ID, second.value.Conversation.ID)
	}
	var activeCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM conversations c JOIN contacts p ON p.id = c.contact_id WHERE p.phone = $1 AND c.status <> 'resolved'`, phone).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 {
		t.Fatalf("active conversation count = %d, want 1", activeCount)
	}
	third, err := app.SimulateInbound(ctx, phone, "depois da corrida", fmt.Sprintf("%s-third", phone))
	if err != nil {
		t.Fatal(err)
	}
	if third.Conversation.ID != first.value.Conversation.ID {
		t.Fatalf("later message used conversation %d, want %d", third.Conversation.ID, first.value.Conversation.ID)
	}
	var contactID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM contacts WHERE phone = $1`, phone).Scan(&contactID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM conversations WHERE contact_id = $1`, contactID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM contacts WHERE id = $1`, contactID)
	})
}

func TestPostgresConcurrentTakeoverAndResolveLeaveResolvedState(t *testing.T) {
	db, ctx := openConcurrencyTestDB(t)
	_, conversation := createConcurrencyFixture(t, db, ctx)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := (ConversationRepository{DB: db}).Takeover(ctx, conversation.ID)
		results <- err
	}()
	go func() {
		<-start
		_, err := (ConversationRepository{DB: db}).Resolve(ctx, conversation.ID)
		results <- err
	}()
	close(start)
	for range 2 {
		if err := <-results; err != nil && err != service.ErrNotFound {
			t.Fatalf("concurrent transition error = %v", err)
		}
	}
	current, err := (ConversationRepository{DB: db}).Get(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != service.StatusResolved {
		t.Fatalf("final status = %s, want resolved", current.Status)
	}
	resolvedAgain, err := (ConversationRepository{DB: db}).Resolve(ctx, conversation.ID)
	if err != nil || resolvedAgain.Status != service.StatusResolved {
		t.Fatalf("repeated resolve = %#v, err %v", resolvedAgain, err)
	}
	if _, err := (ConversationRepository{DB: db}).Takeover(ctx, conversation.ID); err != service.ErrNotFound {
		t.Fatalf("takeover after resolve error = %v, want not found transition", err)
	}
}

func TestPostgresCreateReplyWaitsForConcurrentStateChange(t *testing.T) {
	db, ctx := openConcurrencyTestDB(t)
	_, conversation := createConcurrencyFixture(t, db, ctx)
	inbound, created, err := (MessageRepository{DB: db}).CreateInbound(ctx, conversation.ID, "race", fmt.Sprintf("reply-race-%d", time.Now().UnixNano()))
	if err != nil || !created {
		t.Fatalf("CreateInbound() = created %v, err %v", created, err)
	}

	// Hold the conversation row, start the reply operation, and change state
	// before releasing the lock. SELECT FOR UPDATE must observe the new state.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lockedID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM conversations WHERE id = $1 FOR UPDATE`, conversation.ID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		created bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		_, created, err := (MessageRepository{DB: db}).CreateReply(ctx, conversation.ID, inbound.ID, "late")
		done <- result{created, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE 'SELECT status FROM conversations%')`).Scan(&waiting)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			_ = tx.Rollback()
			t.Fatal("CreateReply() did not wait on the conversation row lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET status = 'human' WHERE id = $1`, conversation.ID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.created {
		t.Fatalf("CreateReply() after takeover = created %v, err %v; want false, nil", got.created, got.err)
	}
	var replies int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE reply_to_message_id = $1`, inbound.ID).Scan(&replies); err != nil {
		t.Fatal(err)
	}
	if replies != 0 {
		t.Fatalf("reply count = %d, want 0", replies)
	}
}

func TestPostgresHumanMessageFromBotTransitionsAtomically(t *testing.T) {
	db, ctx := openConcurrencyTestDB(t)
	_, conversation := createConcurrencyFixture(t, db, ctx)
	app := service.New(ContactRepository{DB: db}, ConversationRepository{DB: db}, MessageRepository{DB: db}, ai.LocalProvider{})

	message, err := app.SendHumanMessage(ctx, conversation.ID, "resposta humana")
	if err != nil {
		t.Fatal(err)
	}
	current, err := (ConversationRepository{DB: db}).Get(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != service.StatusHuman || message.Sender != "human" {
		t.Fatalf("status/message = %s/%#v; want human/human message", current.Status, message)
	}
	var stored int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE conversation_id = $1 AND sender = 'human'`, conversation.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Fatalf("human messages = %d, want 1", stored)
	}
}

func TestPostgresResolveWinningHumanMessageRacePreventsInsert(t *testing.T) {
	db, ctx := openConcurrencyTestDB(t)
	_, conversation := createConcurrencyFixture(t, db, ctx)
	app := service.New(ContactRepository{DB: db}, ConversationRepository{DB: db}, MessageRepository{DB: db}, ai.LocalProvider{})

	// Hold the row while the human-send use case starts. Resolve commits first;
	// the waiting persistence transaction must re-read the resolved state.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var lockedID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM conversations WHERE id = $1 FOR UPDATE`, conversation.ID).Scan(&lockedID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		message service.Message
		err     error
	}
	done := make(chan result, 1)
	go func() {
		message, err := app.SendHumanMessage(ctx, conversation.ID, "não enviar após resolve")
		done <- result{message, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND query LIKE 'SELECT status FROM conversations%')`).Scan(&waiting)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			_ = tx.Rollback()
			t.Fatal("human message transaction did not wait on the conversation row lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET status = 'resolved', updated_at = now() WHERE id = $1`, conversation.ID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if !errors.Is(got.err, service.ErrConversationResolved) {
		t.Fatalf("SendHumanMessage() error = %v, want ErrConversationResolved", got.err)
	}
	var stored int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE conversation_id = $1 AND sender = 'human'`, conversation.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatalf("human messages after resolve = %d, want 0", stored)
	}
}

func TestPostgresHumanMessagePersistenceFailureRollsBackMessageAndState(t *testing.T) {
	db, ctx := openConcurrencyTestDB(t)
	_, conversation := createConcurrencyFixture(t, db, ctx)
	functionName := fmt.Sprintf("reject_human_message_%d", time.Now().UnixNano())
	triggerName := functionName + "_trigger"
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger AS $$
		BEGIN
			IF NEW.id = %d AND NEW.updated_at IS DISTINCT FROM OLD.updated_at THEN
				RAISE EXCEPTION 'injected conversation update failure';
			END IF;
			RETURN NEW;
		END;
	$$ LANGUAGE plpgsql`, functionName, conversation.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE UPDATE ON conversations FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON conversations`, triggerName))
		_, _ = db.ExecContext(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName))
	})

	app := service.New(ContactRepository{DB: db}, ConversationRepository{DB: db}, MessageRepository{DB: db}, ai.LocalProvider{})
	if _, err := app.SendHumanMessage(ctx, conversation.ID, "resposta falha"); err == nil {
		t.Fatal("SendHumanMessage() succeeded despite injected persistence failure")
	}
	current, err := (ConversationRepository{DB: db}).Get(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != service.StatusBot {
		t.Fatalf("status after rollback = %s, want bot", current.Status)
	}
	var stored int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE conversation_id = $1 AND sender = 'human'`, conversation.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Fatalf("human messages after rollback = %d, want 0", stored)
	}
}
