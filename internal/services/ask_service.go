package services

import (
	"context"
	"strings"

	"backend/internal/ai/prompts"
	"backend/internal/domain"
)

const (
	// MaxQuestionLength is the maximum allowed length of an ask query.
	MaxQuestionLength = 500
	// AbstainAnswer is returned when retrieval finds no relevant knowledge base chunks.
	AbstainAnswer = "I couldn't find this in the TradeEdu knowledge base."
)

// AskService handles knowledge-base Q&A with RAG and native citations (story NW-15).
type AskService struct {
	gateway   *AIGateway
	retriever domain.Retriever
}

func NewAskService(gateway *AIGateway, retriever domain.Retriever) *AskService {
	return &AskService{gateway: gateway, retriever: retriever}
}

// Ask answers question from the knowledge base, returning citations to source chunks.
func (s *AskService) Ask(ctx context.Context, userID uint, question string) (*domain.AskResponse, error) {
	trimmed := strings.TrimSpace(question)
	if trimmed == "" {
		return nil, &domain.ValidationError{Msg: "question cannot be empty"}
	}
	if len(question) > MaxQuestionLength {
		return nil, &domain.ValidationError{Msg: "question cannot exceed 500 characters"}
	}

	chunks, err := s.retriever.Search(ctx, trimmed, 5)
	if err != nil {
		return nil, err
	}

	// If retrieval finds 0 hits, return the abstain response without calling the LLM gateway.
	if len(chunks) == 0 {
		return &domain.AskResponse{
			Answer:     AbstainAnswer,
			Citations:  []domain.Citation{},
			Model:      s.gateway.Provider(),
			Disclaimer: domain.AIDisclaimer,
		}, nil
	}

	docs := make([]domain.LLMDocument, len(chunks))
	for i, ch := range chunks {
		docs[i] = domain.LLMDocument{
			Title:     ch.ArticleTitle,
			Content:   ch.Content,
			Citations: true,
		}
	}

	prompt, err := prompts.KnowledgeAsk(trimmed)
	if err != nil {
		return nil, err
	}

	req := domain.LLMRequest{
		Feature:       domain.AIFeatureKnowledgeAsk,
		PromptVersion: prompt.Version,
		System:        prompt.System,
		Messages: []domain.LLMMessage{
			{Role: domain.LLMRoleUser, Text: prompt.User},
		},
		Documents: docs,
		MaxTokens: 2000,
	}

	resp, err := s.gateway.Generate(ctx, userID, req)
	if err != nil {
		return nil, err
	}

	// Map citations back to chunks and validate each quoted_text exists in its chunk.
	var citations []domain.Citation
	for _, c := range resp.Citations {
		if c.DocumentIndex >= 0 && c.DocumentIndex < len(chunks) {
			ch := chunks[c.DocumentIndex]
			if strings.Contains(ch.Content, c.CitedText) {
				citations = append(citations, domain.Citation{
					ArticleSlug:  ch.ArticleSlug,
					ArticleTitle: ch.ArticleTitle,
					ChunkID:      ch.ChunkID,
					QuotedText:   c.CitedText,
				})
			}
		}
	}
	if citations == nil {
		citations = []domain.Citation{}
	}

	return &domain.AskResponse{
		Answer:     resp.Content,
		Citations:  citations,
		Model:      resp.Model,
		Disclaimer: domain.AIDisclaimer,
	}, nil
}
