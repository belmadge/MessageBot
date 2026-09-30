package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/portfolio/whatsapp-support/internal/service"
)

type ContactRepository struct {
	DB *sql.DB
}

type ConversationRepository struct {
	DB *sql.DB
}

type MessageRepository struct {
	DB *sql.DB
}

func (r ContactRepository) FindOrCreateByPhone(ctx context.Context, phone string) (service.Contact, error) {
	var contact service.Contact
	err := r.DB.QueryRowContext(ctx, `INSERT INTO contacts(phone) VALUES ($1)
		ON CONFLICT (phone) DO UPDATE SET phone = EXCLUDED.phone
		RETURNING id, phone, COALESCE(name, ''), created_at`, phone).Scan(&contact.ID, &contact.Phone, &contact.Name, &contact.CreatedAt)
	return contact, err
}

func (r ConversationRepository) FindActiveByContact(ctx context.Context, contactID int64) (service.Conversation, error) {
	conversation, err := scanConversation(r.DB.QueryRowContext(ctx, `SELECT c.id, c.contact_id, p.phone, c.status, c.created_at, c.updated_at
		FROM conversations c JOIN contacts p ON p.id = c.contact_id
		WHERE c.contact_id = $1 AND c.status <> 'resolved' ORDER BY c.updated_at DESC LIMIT 1`, contactID))
	return conversation, translateNotFound(err)
}

func (r ConversationRepository) Create(ctx context.Context, contactID int64) (service.Conversation, error) {
	for {
		conversation, err := scanConversation(r.DB.QueryRowContext(ctx, `INSERT INTO conversations(contact_id) VALUES ($1)
			ON CONFLICT (contact_id) WHERE status <> 'resolved' DO NOTHING
			RETURNING id, contact_id, (SELECT phone FROM contacts WHERE id = $1), status, created_at, updated_at`, contactID))
		if !errors.Is(err, sql.ErrNoRows) {
			return conversation, err
		}
		conversation, err = r.FindActiveByContact(ctx, contactID)
		if !errors.Is(err, service.ErrNotFound) {
			return conversation, err
		}
		// The conflicting active conversation may have been resolved before it
		// could be loaded. Retry so this inbound can start a new active one.
	}
}

func (r ConversationRepository) Get(ctx context.Context, id int64) (service.Conversation, error) {
	conversation, err := scanConversation(r.DB.QueryRowContext(ctx, `SELECT c.id, c.contact_id, p.phone, c.status, c.created_at, c.updated_at
		FROM conversations c JOIN contacts p ON p.id = c.contact_id WHERE c.id = $1`, id))
	return conversation, translateNotFound(err)
}

func (r ConversationRepository) List(ctx context.Context, status service.Status) ([]service.Conversation, error) {
	query := `SELECT c.id, c.contact_id, p.phone, c.status, c.created_at, c.updated_at FROM conversations c JOIN contacts p ON p.id = c.contact_id`
	args := []any{}
	if status != "" {
		query += ` WHERE c.status = $1`
		args = append(args, status)
	}
	query += ` ORDER BY c.updated_at DESC, c.id DESC`
	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]service.Conversation, 0)
	for rows.Next() {
		item, scanErr := scanConversation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r ConversationRepository) Takeover(ctx context.Context, id int64) (service.Conversation, error) {
	conversation, err := scanConversation(r.DB.QueryRowContext(ctx, `UPDATE conversations c SET status = 'human', updated_at = now()
		FROM contacts p WHERE c.contact_id = p.id AND c.id = $1 AND c.status = 'bot'
		RETURNING c.id, c.contact_id, p.phone, c.status, c.created_at, c.updated_at`, id))
	return conversation, translateNotFound(err)
}

func (r ConversationRepository) Resolve(ctx context.Context, id int64) (service.Conversation, error) {
	conversation, err := scanConversation(r.DB.QueryRowContext(ctx, `UPDATE conversations c SET status = 'resolved', updated_at = now()
		FROM contacts p WHERE c.contact_id = p.id AND c.id = $1 AND c.status IN ('bot', 'human')
		RETURNING c.id, c.contact_id, p.phone, c.status, c.created_at, c.updated_at`, id))
	if err == nil || !errors.Is(err, sql.ErrNoRows) {
		return conversation, translateNotFound(err)
	}
	conversation, err = r.Get(ctx, id)
	if err != nil {
		return service.Conversation{}, err
	}
	if conversation.Status == service.StatusResolved {
		return conversation, nil
	}
	return service.Conversation{}, service.ErrNotFound
}

func (r MessageRepository) Create(ctx context.Context, conversationID int64, direction, content, sender string) (service.Message, error) {
	var message service.Message
	err := r.DB.QueryRowContext(ctx, `INSERT INTO messages(conversation_id, direction, content, sender)
		VALUES ($1, $2, $3, $4) RETURNING id, conversation_id, direction, content, sender, created_at`,
		conversationID, direction, content, sender).Scan(&message.ID, &message.ConversationID, &message.Direction, &message.Content, &message.Sender, &message.CreatedAt)
	if err == nil {
		_, err = r.DB.ExecContext(ctx, `UPDATE conversations SET updated_at = $2 WHERE id = $1`, conversationID, message.CreatedAt)
	}
	return message, err
}

// CreateHumanMessage atomically transitions an active conversation to human,
// records the outbound message, and advances updated_at. The row lock makes a
// concurrent resolve serialize either before or after this complete operation.
func (r MessageRepository) CreateHumanMessage(ctx context.Context, conversationID int64, content string) (service.Message, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return service.Message{}, err
	}
	defer tx.Rollback()

	var status service.Status
	if err := tx.QueryRowContext(ctx, `SELECT status FROM conversations WHERE id = $1 FOR UPDATE`, conversationID).Scan(&status); err != nil {
		return service.Message{}, translateNotFound(err)
	}
	if status == service.StatusResolved {
		return service.Message{}, service.ErrConversationResolved
	}
	if status != service.StatusBot && status != service.StatusHuman {
		return service.Message{}, service.ErrNotFound
	}

	var message service.Message
	err = tx.QueryRowContext(ctx, `INSERT INTO messages(conversation_id, direction, content, sender, created_at)
		VALUES ($1, 'outbound', $2, 'human', clock_timestamp())
		RETURNING id, conversation_id, direction, content, sender, created_at`,
		conversationID, content).Scan(&message.ID, &message.ConversationID, &message.Direction, &message.Content, &message.Sender, &message.CreatedAt)
	if err != nil {
		return service.Message{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET status = 'human', updated_at = GREATEST(updated_at, $2, clock_timestamp()) WHERE id = $1`, conversationID, message.CreatedAt); err != nil {
		return service.Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return service.Message{}, err
	}
	return message, nil
}

func (r MessageRepository) CreateInbound(ctx context.Context, conversationID int64, content, externalID string) (service.Message, bool, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return service.Message{}, false, err
	}
	defer tx.Rollback()

	var message service.Message
	err = tx.QueryRowContext(ctx, `INSERT INTO messages(conversation_id, direction, content, sender, external_id)
		VALUES ($1, 'inbound', $2, 'contact', $3)
		ON CONFLICT (external_id) DO NOTHING
		RETURNING id, conversation_id, direction, content, sender, external_id, created_at`,
		conversationID, content, externalID).Scan(&message.ID, &message.ConversationID, &message.Direction, &message.Content, &message.Sender, &message.ExternalID, &message.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Message{}, false, nil
	}
	if err != nil {
		return service.Message{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET updated_at = $2 WHERE id = $1`, conversationID, message.CreatedAt); err != nil {
		return service.Message{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return service.Message{}, false, err
	}
	return message, true, nil
}

func (r MessageRepository) FindInboundByExternalID(ctx context.Context, externalID string) (service.Message, error) {
	message, err := scanMessage(r.DB.QueryRowContext(ctx, `SELECT id, conversation_id, direction, content, sender, external_id, reply_to_message_id, created_at
		FROM messages WHERE direction = 'inbound' AND external_id = $1`, externalID))
	return message, translateNotFound(err)
}

func (r MessageRepository) CreateReply(ctx context.Context, conversationID, inboundMessageID int64, content string) (service.Message, bool, error) {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return service.Message{}, false, err
	}
	defer tx.Rollback()
	var status service.Status
	if err := tx.QueryRowContext(ctx, `SELECT status FROM conversations WHERE id = $1 FOR UPDATE`, conversationID).Scan(&status); err != nil {
		return service.Message{}, false, translateNotFound(err)
	}
	if status != service.StatusBot {
		return service.Message{}, false, nil
	}
	var message service.Message
	var replyToID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO messages(conversation_id, direction, content, sender, reply_to_message_id)
		VALUES ($1, 'outbound', $2, 'ai', $3)
		ON CONFLICT (reply_to_message_id) WHERE reply_to_message_id IS NOT NULL DO NOTHING
		RETURNING id, conversation_id, direction, content, sender, reply_to_message_id, created_at`,
		conversationID, content, inboundMessageID).Scan(&message.ID, &message.ConversationID, &message.Direction, &message.Content, &message.Sender, &replyToID, &message.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return service.Message{}, false, nil
	}
	if err != nil {
		return service.Message{}, false, err
	}
	message.ReplyToID = &replyToID
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET updated_at = $2 WHERE id = $1`, conversationID, message.CreatedAt); err != nil {
		return service.Message{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return service.Message{}, false, err
	}
	return message, true, nil
}

func (r MessageRepository) FindReplyByInboundID(ctx context.Context, inboundMessageID int64) (*service.Message, error) {
	message, err := scanMessage(r.DB.QueryRowContext(ctx, `SELECT id, conversation_id, direction, content, sender, external_id, reply_to_message_id, created_at
		FROM messages WHERE reply_to_message_id = $1`, inboundMessageID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &message, nil
}

func (r MessageRepository) ListByConversation(ctx context.Context, conversationID int64) ([]service.Message, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT id, conversation_id, direction, content, sender, external_id, reply_to_message_id, created_at
		FROM messages WHERE conversation_id = $1 ORDER BY created_at, id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]service.Message, 0)
	for rows.Next() {
		item, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r MessageRepository) ClaimInboundProcessing(ctx context.Context, inboundID int64) (time.Time, bool, error) {
	var claimedAt time.Time
	err := r.DB.QueryRowContext(ctx, `UPDATE messages m SET processing_started_at = clock_timestamp()
		WHERE m.id = $1 AND m.direction = 'inbound'
		  AND (m.processing_started_at IS NULL OR m.processing_started_at < clock_timestamp() - interval '1 minute')
		  AND NOT EXISTS (SELECT 1 FROM messages reply WHERE reply.reply_to_message_id = m.id)
		RETURNING m.processing_started_at`, inboundID).Scan(&claimedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return claimedAt, true, nil
}

func (r MessageRepository) ReleaseInboundProcessing(ctx context.Context, inboundID int64, claimedAt time.Time) error {
	_, err := r.DB.ExecContext(ctx, `UPDATE messages SET processing_started_at = NULL
		WHERE id = $1 AND processing_started_at = $2`, inboundID, claimedAt)
	return err
}

type rowScanner interface {
	Scan(...any) error
}

func scanMessage(row rowScanner) (service.Message, error) {
	var message service.Message
	var externalID sql.NullString
	var replyToID sql.NullInt64
	err := row.Scan(&message.ID, &message.ConversationID, &message.Direction, &message.Content, &message.Sender, &externalID, &replyToID, &message.CreatedAt)
	if externalID.Valid {
		message.ExternalID = externalID.String
	}
	if replyToID.Valid {
		message.ReplyToID = &replyToID.Int64
	}
	return message, err
}

func scanConversation(row rowScanner) (service.Conversation, error) {
	var item service.Conversation
	var status string
	err := row.Scan(&item.ID, &item.ContactID, &item.Phone, &status, &item.CreatedAt, &item.UpdatedAt)
	item.Status = service.Status(status)
	return item, err
}

func translateNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrNotFound
	}
	return err
}
