package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPostgresInboundProcessingClaimSerializesConcurrentAttempts(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect to test PostgreSQL: %v", err)
	}

	suffix := time.Now().UnixNano()
	contact, err := (ContactRepository{DB: db}).FindOrCreateByPhone(ctx, fmt.Sprintf("validation-%d", suffix))
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := (ConversationRepository{DB: db}).Create(ctx, contact.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM conversations WHERE id = $1`, conversation.ID)
		_, _ = db.ExecContext(cleanupCtx, `DELETE FROM contacts WHERE id = $1`, contact.ID)
	})

	message, created, err := (MessageRepository{DB: db}).CreateInbound(ctx, conversation.ID, "concurrent retry test", fmt.Sprintf("validation-event-%d", suffix))
	if err != nil || !created {
		t.Fatalf("CreateInbound() = created %v, err %v", created, err)
	}

	const attempts = 12
	start := make(chan struct{})
	claimed := make(chan time.Time, attempts)
	errorsFound := make(chan error, attempts)
	var workers sync.WaitGroup
	for range attempts {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			claimAt, ok, err := (MessageRepository{DB: db}).ClaimInboundProcessing(ctx, message.ID)
			if err != nil {
				errorsFound <- err
				return
			}
			if ok {
				claimed <- claimAt
			}
		}()
	}
	close(start)
	workers.Wait()
	close(claimed)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("ClaimInboundProcessing() error: %v", err)
	}
	var claimAt time.Time
	claimCount := 0
	for value := range claimed {
		claimAt = value
		claimCount++
	}
	if claimCount != 1 {
		t.Fatalf("successful claims = %d, want exactly one", claimCount)
	}

	repository := MessageRepository{DB: db}
	if err := repository.ReleaseInboundProcessing(ctx, message.ID, claimAt); err != nil {
		t.Fatal(err)
	}
	retryAt, ok, err := repository.ClaimInboundProcessing(ctx, message.ID)
	if err != nil || !ok {
		t.Fatalf("retry claim = %v, err %v; want claim after release", ok, err)
	}
	if _, created, err := repository.CreateReply(ctx, conversation.ID, message.ID, "single persisted reply"); err != nil || !created {
		t.Fatalf("CreateReply() = created %v, err %v; want true, nil", created, err)
	}
	if _, created, err := repository.CreateReply(ctx, conversation.ID, message.ID, "duplicate reply"); err != nil || created {
		t.Fatalf("CreateReply() duplicate = created %v, err %v; want false, nil", created, err)
	}
	if err := repository.ReleaseInboundProcessing(ctx, message.ID, retryAt); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repository.ClaimInboundProcessing(ctx, message.ID); err != nil || ok {
		t.Fatalf("claim after reply = %v, err %v; want no further claim", ok, err)
	}
	var replies int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE reply_to_message_id = $1`, message.ID).Scan(&replies); err != nil {
		t.Fatal(err)
	}
	if replies != 1 {
		t.Fatalf("replies for inbound = %d, want exactly one", replies)
	}
}
