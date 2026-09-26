package memories

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/leofelipet/contexta/internal/embeddings"
)

var (
	ErrInvalidContent      = errors.New("invalid memory content")
	ErrEmbedderUnavailable = errors.New("embeddings provider unavailable")
)

type Store interface {
	ListMemories(context.Context, ListParams) (Page, error)
	GetMemory(context.Context, string) (Memory, error)
	ResolveMessageMemoryContent(ctx context.Context, messageID string) (content, conversationID, contactID string, err error)
	CreateMemory(ctx context.Context, params CreateParams, embedding []float32, model string, embedErr error) (Memory, error)
	UpdateMemory(ctx context.Context, id string, params UpdateParams, embedding []float32, model string, embedErr error, contentChanged bool) (Memory, error)
	DeleteMemory(context.Context, string) error
	SearchMemories(ctx context.Context, params SearchParams, queryEmbedding []float32) (SearchResult, error)
}

type Service struct {
	store    Store
	embedder embeddings.Client
}

func NewService(store Store, embedder embeddings.Client) *Service {
	return &Service{store: store, embedder: embedder}
}

func (s *Service) List(ctx context.Context, params ListParams) (Page, error) {
	return s.store.ListMemories(ctx, params)
}

func (s *Service) Get(ctx context.Context, id string) (Memory, error) {
	return s.store.GetMemory(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.DeleteMemory(ctx, id)
}

func (s *Service) Create(ctx context.Context, params CreateParams) (Memory, error) {
	params, err := s.normalizeCreate(ctx, params)
	if err != nil {
		return Memory{}, err
	}
	embedding, model, embedErr := s.embedOne(ctx, params.Content)
	return s.store.CreateMemory(ctx, params, embedding, model, embedErr)
}

func (s *Service) Update(ctx context.Context, id string, params UpdateParams) (Memory, error) {
	current, err := s.store.GetMemory(ctx, id)
	if err != nil {
		return Memory{}, err
	}
	contentChanged := params.Content != nil && strings.TrimSpace(*params.Content) != current.Content
	var embedding []float32
	var model string
	var embedErr error
	if contentChanged {
		content := strings.TrimSpace(*params.Content)
		if content == "" || utf8.RuneCountInString(content) > MaxContentRunes {
			return Memory{}, ErrInvalidContent
		}
		embedding, model, embedErr = s.embedOne(ctx, content)
	}
	return s.store.UpdateMemory(ctx, id, params, embedding, model, embedErr, contentChanged)
}

func (s *Service) Search(ctx context.Context, params SearchParams) (SearchResult, error) {
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return SearchResult{}, ErrInvalidContent
	}
	embedding, _, err := s.embedOne(ctx, query)
	if err != nil {
		return SearchResult{}, err
	}
	params.Query = query
	return s.store.SearchMemories(ctx, params, embedding)
}

func (s *Service) normalizeCreate(ctx context.Context, params CreateParams) (CreateParams, error) {
	params.Title = strings.TrimSpace(params.Title)
	params.Content = strings.TrimSpace(params.Content)
	params.MessageID = strings.TrimSpace(params.MessageID)
	params.ConversationID = strings.TrimSpace(params.ConversationID)
	params.ContactID = strings.TrimSpace(params.ContactID)
	params.Source = strings.TrimSpace(params.Source)

	if params.MessageID != "" {
		content, conversationID, contactID, err := s.store.ResolveMessageMemoryContent(ctx, params.MessageID)
		if err != nil {
			return CreateParams{}, err
		}
		if params.Content == "" {
			params.Content = content
		}
		if params.ConversationID == "" {
			params.ConversationID = conversationID
		}
		if params.ContactID == "" {
			params.ContactID = contactID
		}
		params.Source = SourceMessage
		if params.Title == "" {
			params.Title = "Mensagem salva"
		}
	}
	if params.Source == "" {
		params.Source = SourceNote
	}
	if params.Content == "" || utf8.RuneCountInString(params.Content) > MaxContentRunes {
		return CreateParams{}, ErrInvalidContent
	}
	return params, nil
}

func (s *Service) embedOne(ctx context.Context, text string) ([]float32, string, error) {
	if s.embedder == nil {
		return nil, "", ErrEmbedderUnavailable
	}
	if available, ok := s.embedder.(interface{ Available() bool }); ok && !available.Available() {
		return nil, s.embedder.Model(), ErrEmbedderUnavailable
	}
	vectors, err := s.embedder.Embed(ctx, []string{text})
	model := s.embedder.Model()
	if err != nil {
		return nil, model, err
	}
	if len(vectors) != 1 {
		return nil, model, ErrEmbedderUnavailable
	}
	return vectors[0], model, nil
}
