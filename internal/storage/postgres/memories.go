package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/leofelipet/contexta/internal/memories"
	"github.com/leofelipet/contexta/internal/pagination"
	"github.com/pgvector/pgvector-go"
)

const memorySelectCols = `
	m.id::text, m.title, m.content, m.source,
	COALESCE(m.message_id::text, ''), COALESCE(m.conversation_id::text, ''), COALESCE(m.contact_id::text, ''),
	COALESCE(NULLIF(c.title, ''), NULLIF(cc.name, ''), NULLIF(cc.push_name, ''), NULLIF(cc.phone, ''), ''),
	COALESCE(NULLIF(ct.name, ''), NULLIF(ct.push_name, ''), NULLIF(ct.phone, ''), ''),
	m.embedding_status, m.embedding_model, m.embedding_error, m.created_at, m.updated_at`

const memoryJoins = `
	FROM memories m
	LEFT JOIN conversations c ON c.id = m.conversation_id
	LEFT JOIN contacts cc ON cc.id = c.contact_id
	LEFT JOIN contacts ct ON ct.id = m.contact_id`

func (s *Store) ListMemories(ctx context.Context, params memories.ListParams) (memories.Page, error) {
	limit := normalizeLimit(params.Limit, 50)
	cursor, err := pagination.Decode(params.Cursor, "memories")
	if err != nil {
		return memories.Page{}, err
	}
	if params.Cursor != "" && (cursor.Time.IsZero() || !isUUID(cursor.ID)) {
		return memories.Page{}, pagination.ErrInvalidCursor
	}
	if params.ContactID != "" && !isUUID(params.ContactID) {
		return memories.Page{}, ErrInvalidArgument
	}
	if params.ConversationID != "" && !isUUID(params.ConversationID) {
		return memories.Page{}, ErrInvalidArgument
	}
	if params.Source != "" && !memories.ValidSource(params.Source) {
		return memories.Page{}, ErrInvalidArgument
	}

	query := strings.TrimSpace(params.Query)
	rows, err := s.pool.Query(ctx, `
		SELECT `+memorySelectCols+memoryJoins+`
		WHERE ($1::timestamptz IS NULL OR (m.created_at, m.id) < ($1, $2::uuid))
		  AND ($3::uuid IS NULL OR m.contact_id = $3::uuid)
		  AND ($4::uuid IS NULL OR m.conversation_id = $4::uuid)
		  AND ($5 = '' OR m.source = $5)
		  AND ($6 = '' OR m.title ILIKE '%' || $6 || '%' OR m.content ILIKE '%' || $6 || '%')
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $7`,
		nullableTime(cursor.Time), nullableUUID(cursor.ID),
		nullableUUID(params.ContactID), nullableUUID(params.ConversationID),
		params.Source, query, limit+1,
	)
	if err != nil {
		return memories.Page{}, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()

	items := make([]memories.Memory, 0, limit+1)
	for rows.Next() {
		item, err := scanMemory(rows)
		if err != nil {
			return memories.Page{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return memories.Page{}, fmt.Errorf("iterate memories: %w", err)
	}

	page := memories.Page{Memories: items[:min(limit, len(items))]}
	if len(items) > limit {
		last := items[limit-1]
		page.NextCursor = pagination.Encode(pagination.Cursor{Kind: "memories", Time: last.CreatedAt, ID: last.ID})
	}
	return page, nil
}

func (s *Store) GetMemory(ctx context.Context, id string) (memories.Memory, error) {
	if !isUUID(id) {
		return memories.Memory{}, ErrInvalidArgument
	}
	row := s.pool.QueryRow(ctx, `SELECT `+memorySelectCols+memoryJoins+` WHERE m.id = $1`, id)
	item, err := scanMemory(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return memories.Memory{}, ErrNotFound
	}
	if err != nil {
		return memories.Memory{}, fmt.Errorf("get memory: %w", err)
	}
	return item, nil
}

func (s *Store) ResolveMessageMemoryContent(ctx context.Context, messageID string) (content, conversationID, contactID string, err error) {
	if !isUUID(messageID) {
		return "", "", "", ErrInvalidArgument
	}
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(m.text, ''), NULLIF(m.transcription_text, ''), ''),
		       m.conversation_id::text,
		       COALESCE(c.contact_id::text, '')
		FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		WHERE m.id = $1`, messageID).Scan(&content, &conversationID, &contactID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", ErrNotFound
	}
	if err != nil {
		return "", "", "", fmt.Errorf("resolve message memory content: %w", err)
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", "", "", ErrInvalidArgument
	}
	return content, conversationID, contactID, nil
}

func (s *Store) CreateMemory(ctx context.Context, params memories.CreateParams, embedding []float32, model string, embedErr error) (memories.Memory, error) {
	title := strings.TrimSpace(params.Title)
	content := strings.TrimSpace(params.Content)
	source := strings.TrimSpace(params.Source)
	if source == "" {
		source = memories.SourceNote
	}
	if !memories.ValidSource(source) {
		return memories.Memory{}, ErrInvalidArgument
	}
	if content == "" || utf8.RuneCountInString(content) > memories.MaxContentRunes {
		return memories.Memory{}, ErrInvalidArgument
	}
	if len(title) > 300 {
		return memories.Memory{}, ErrInvalidArgument
	}

	messageID := strings.TrimSpace(params.MessageID)
	conversationID := strings.TrimSpace(params.ConversationID)
	contactID := strings.TrimSpace(params.ContactID)
	if messageID != "" {
		if !isUUID(messageID) {
			return memories.Memory{}, ErrInvalidArgument
		}
		source = memories.SourceMessage
	}
	if conversationID != "" {
		if !isUUID(conversationID) {
			return memories.Memory{}, ErrInvalidArgument
		}
		exists, err := s.entityExists(ctx, "conversations", conversationID)
		if err != nil {
			return memories.Memory{}, err
		}
		if !exists {
			return memories.Memory{}, ErrNotFound
		}
	}
	if contactID != "" {
		if !isUUID(contactID) {
			return memories.Memory{}, ErrInvalidArgument
		}
		exists, err := s.entityExists(ctx, "contacts", contactID)
		if err != nil {
			return memories.Memory{}, err
		}
		if !exists {
			return memories.Memory{}, ErrNotFound
		}
	}

	status := memories.EmbeddingReady
	errorText := ""
	var vector any
	if embedErr != nil {
		status = memories.EmbeddingFailed
		errorText = truncateErr(embedErr.Error())
	} else if len(embedding) != memories.EmbeddingDimensions {
		status = memories.EmbeddingFailed
		errorText = "invalid embedding dimensions"
	} else {
		vector = pgvector.NewVector(embedding)
	}

	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO memories (
			title, content, source, message_id, conversation_id, contact_id,
			embedding, embedding_status, embedding_model, embedding_error
		) VALUES (
			$1, $2, $3, $4::uuid, $5::uuid, $6::uuid,
			$7, $8, $9, $10
		) RETURNING id::text`,
		title, content, source,
		nullableUUID(messageID), nullableUUID(conversationID), nullableUUID(contactID),
		vector, status, model, errorText,
	).Scan(&id)
	if err != nil {
		return memories.Memory{}, fmt.Errorf("create memory: %w", err)
	}
	return s.GetMemory(ctx, id)
}

func (s *Store) UpdateMemory(ctx context.Context, id string, params memories.UpdateParams, embedding []float32, model string, embedErr error, contentChanged bool) (memories.Memory, error) {
	if !isUUID(id) {
		return memories.Memory{}, ErrInvalidArgument
	}
	current, err := s.GetMemory(ctx, id)
	if err != nil {
		return memories.Memory{}, err
	}

	title := current.Title
	if params.Title != nil {
		title = strings.TrimSpace(*params.Title)
		if len(title) > 300 {
			return memories.Memory{}, ErrInvalidArgument
		}
	}
	content := current.Content
	if params.Content != nil {
		content = strings.TrimSpace(*params.Content)
		if content == "" || utf8.RuneCountInString(content) > memories.MaxContentRunes {
			return memories.Memory{}, ErrInvalidArgument
		}
	}
	conversationID := current.ConversationID
	if params.ConversationID != nil {
		conversationID = strings.TrimSpace(*params.ConversationID)
		if conversationID != "" {
			if !isUUID(conversationID) {
				return memories.Memory{}, ErrInvalidArgument
			}
			exists, err := s.entityExists(ctx, "conversations", conversationID)
			if err != nil {
				return memories.Memory{}, err
			}
			if !exists {
				return memories.Memory{}, ErrNotFound
			}
		}
	}
	contactID := current.ContactID
	if params.ContactID != nil {
		contactID = strings.TrimSpace(*params.ContactID)
		if contactID != "" {
			if !isUUID(contactID) {
				return memories.Memory{}, ErrInvalidArgument
			}
			exists, err := s.entityExists(ctx, "contacts", contactID)
			if err != nil {
				return memories.Memory{}, err
			}
			if !exists {
				return memories.Memory{}, ErrNotFound
			}
		}
	}

	status := current.EmbeddingStatus
	errorText := current.EmbeddingError
	embedModel := current.EmbeddingModel
	var vector any
	updateEmbedding := contentChanged
	if updateEmbedding {
		if embedErr != nil {
			status = memories.EmbeddingFailed
			errorText = truncateErr(embedErr.Error())
			embedModel = model
			vector = nil
		} else if len(embedding) != memories.EmbeddingDimensions {
			status = memories.EmbeddingFailed
			errorText = "invalid embedding dimensions"
			embedModel = model
			vector = nil
		} else {
			status = memories.EmbeddingReady
			errorText = ""
			embedModel = model
			vector = pgvector.NewVector(embedding)
		}
	}

	if updateEmbedding {
		tag, err := s.pool.Exec(ctx, `
			UPDATE memories SET
				title = $2,
				content = $3,
				conversation_id = $4::uuid,
				contact_id = $5::uuid,
				embedding = $6,
				embedding_status = $7,
				embedding_model = $8,
				embedding_error = $9,
				updated_at = now()
			WHERE id = $1`,
			id, title, content,
			nullableUUID(conversationID), nullableUUID(contactID),
			vector, status, embedModel, errorText,
		)
		if err != nil {
			return memories.Memory{}, fmt.Errorf("update memory: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return memories.Memory{}, ErrNotFound
		}
	} else {
		tag, err := s.pool.Exec(ctx, `
			UPDATE memories SET
				title = $2,
				content = $3,
				conversation_id = $4::uuid,
				contact_id = $5::uuid,
				updated_at = now()
			WHERE id = $1`,
			id, title, content,
			nullableUUID(conversationID), nullableUUID(contactID),
		)
		if err != nil {
			return memories.Memory{}, fmt.Errorf("update memory: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return memories.Memory{}, ErrNotFound
		}
	}
	return s.GetMemory(ctx, id)
}

func (s *Store) DeleteMemory(ctx context.Context, id string) error {
	if !isUUID(id) {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM memories WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SearchMemories(ctx context.Context, params memories.SearchParams, queryEmbedding []float32) (memories.SearchResult, error) {
	if strings.TrimSpace(params.Query) == "" {
		return memories.SearchResult{}, ErrInvalidArgument
	}
	if len(queryEmbedding) != memories.EmbeddingDimensions {
		return memories.SearchResult{}, ErrInvalidArgument
	}
	if params.ContactID != "" && !isUUID(params.ContactID) {
		return memories.SearchResult{}, ErrInvalidArgument
	}
	if params.ConversationID != "" && !isUUID(params.ConversationID) {
		return memories.SearchResult{}, ErrInvalidArgument
	}
	limit := normalizeLimit(params.Limit, 20)
	if limit > 50 {
		limit = 50
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+memorySelectCols+`,
		       (1 - (m.embedding <=> $1))::float8 AS score
		`+memoryJoins+`
		WHERE m.embedding IS NOT NULL
		  AND m.embedding_status = 'ready'
		  AND ($2::uuid IS NULL OR m.contact_id = $2::uuid)
		  AND ($3::uuid IS NULL OR m.conversation_id = $3::uuid)
		ORDER BY m.embedding <=> $1
		LIMIT $4`,
		pgvector.NewVector(queryEmbedding),
		nullableUUID(params.ContactID),
		nullableUUID(params.ConversationID),
		limit,
	)
	if err != nil {
		return memories.SearchResult{}, fmt.Errorf("search memories: %w", err)
	}
	defer rows.Close()

	hits := make([]memories.SearchHit, 0, limit)
	for rows.Next() {
		var hit memories.SearchHit
		item, score, err := scanMemoryWithScore(rows)
		if err != nil {
			return memories.SearchResult{}, err
		}
		hit.Memory = item
		hit.Score = score
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return memories.SearchResult{}, fmt.Errorf("iterate memory search: %w", err)
	}
	return memories.SearchResult{Hits: hits}, nil
}

type memoryScanner interface {
	Scan(dest ...any) error
}

func scanMemory(row memoryScanner) (memories.Memory, error) {
	var item memories.Memory
	if err := row.Scan(
		&item.ID, &item.Title, &item.Content, &item.Source,
		&item.MessageID, &item.ConversationID, &item.ContactID,
		&item.ConversationTitle, &item.ContactName,
		&item.EmbeddingStatus, &item.EmbeddingModel, &item.EmbeddingError,
		&item.CreatedAt, &item.UpdatedAt,
	); err != nil {
		return memories.Memory{}, err
	}
	return item, nil
}

func scanMemoryWithScore(row memoryScanner) (memories.Memory, float64, error) {
	var item memories.Memory
	var score float64
	if err := row.Scan(
		&item.ID, &item.Title, &item.Content, &item.Source,
		&item.MessageID, &item.ConversationID, &item.ContactID,
		&item.ConversationTitle, &item.ContactName,
		&item.EmbeddingStatus, &item.EmbeddingModel, &item.EmbeddingError,
		&item.CreatedAt, &item.UpdatedAt, &score,
	); err != nil {
		return memories.Memory{}, 0, err
	}
	return item, score, nil
}

func truncateErr(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		return value[:500]
	}
	return value
}
