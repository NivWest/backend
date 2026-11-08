package fake

import (
	"fmt"
	"strings"

	"backend/internal/domain"
)

// knowledgeAsk answers knowledge_ask requests per the fake provider contract:
// picks the top retrieved chunk, answers "FAKE: According to \"<article title>\": <first sentence of chunk>",
// and returns one citation whose quoted_text is that first sentence.
func knowledgeAsk(req domain.LLMRequest) (string, []domain.LLMCitation, error) {
	if len(req.Documents) == 0 {
		return "I couldn't find this in the TradeEdu knowledge base.", nil, nil
	}

	topDoc := req.Documents[0]
	sentence := firstSentence(topDoc.Content)
	if sentence == "" {
		sentence = topDoc.Content
	}

	answer := fmt.Sprintf("FAKE: According to %q: %s", topDoc.Title, sentence)

	start := strings.Index(topDoc.Content, sentence)
	end := start + len(sentence)
	if start < 0 {
		start, end = 0, 0
	}

	citation := domain.LLMCitation{
		DocumentIndex:  0,
		DocumentTitle:  topDoc.Title,
		CitedText:      sentence,
		StartCharIndex: start,
		EndCharIndex:   end,
	}

	return answer, []domain.LLMCitation{citation}, nil
}

// firstSentence extracts the first sentence of text, preserving exact substring characters.
func firstSentence(text string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return ""
	}
	end := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '.' || s[i] == '!' || s[i] == '?' {
			if i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\n' || s[i+1] == '\r' || s[i+1] == '\t' {
				end = i + 1
				break
			}
		}
	}
	if end != -1 {
		return strings.TrimSpace(s[:end])
	}
	if idx := strings.Index(s, "\n"); idx != -1 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}
