package server

import (
	"encoding/json"
	"io"
	"net/http"
	"unicode/utf8"
)

// Token estimation constants. Counts are estimates in the spirit of the
// upstream proxy (Copilot itself has no Anthropic tokenizer); Claude Code only
// uses them for compaction heuristics.
const (
	charsPerToken      = 4
	perMessageOverhead = 4
	imageCharEstimate  = 1600
	minimumInputTokens = 1
)

type countTokensRequest struct {
	System   json.RawMessage   `json:"system"`
	Messages []json.RawMessage `json:"messages"`
	Tools    json.RawMessage   `json:"tools"`
}

type countTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, errTypeInvalidRequest,
			"failed to read request body: "+err.Error())
		return
	}
	var request countTokensRequest
	if parseErr := json.Unmarshal(body, &request); parseErr != nil {
		writeAnthropicError(w, http.StatusBadRequest, errTypeInvalidRequest,
			"request body is not valid JSON")
		return
	}
	writeJSON(w, http.StatusOK, countTokensResponse{InputTokens: estimateInputTokens(request)})
}

// estimateInputTokens approximates the prompt size: one token per four
// characters of text plus a small per-message overhead. Thinking blocks are
// excluded, matching how Anthropic counts input tokens.
func estimateInputTokens(request countTokensRequest) int {
	chars := contentChars(request.System)
	chars += textChars(string(request.Tools))
	for _, raw := range request.Messages {
		var message struct {
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(raw, &message) != nil {
			continue
		}
		chars += contentChars(message.Content)
	}
	tokens := (chars+charsPerToken-1)/charsPerToken + perMessageOverhead*len(request.Messages)
	return max(tokens, minimumInputTokens)
}

// textChars weights non-ASCII runes as a full token's worth of characters:
// CJK and similar scripts tokenize at roughly one token per rune, so counting
// their UTF-8 bytes (2-4 per rune) at 4 chars/token would underestimate them
// badly enough to defer Claude Code's auto-compaction past the prompt limit.
func textChars(text string) int {
	chars := 0
	for _, r := range text {
		if r < utf8.RuneSelf {
			chars++
		} else {
			chars += charsPerToken
		}
	}
	return chars
}

type estimatedBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	Content json.RawMessage `json:"content"`
}

// contentChars counts the characters of an Anthropic content value, which is
// either a plain string or an array of typed blocks.
func contentChars(content json.RawMessage) int {
	if len(content) == 0 {
		return 0
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return textChars(text)
	}
	var blocks []estimatedBlock
	if json.Unmarshal(content, &blocks) != nil {
		return 0
	}
	total := 0
	for _, block := range blocks {
		total += blockChars(block)
	}
	return total
}

func blockChars(block estimatedBlock) int {
	switch block.Type {
	case "text":
		return textChars(block.Text)
	case "tool_use":
		return len(block.Name) + textChars(string(block.Input))
	case "tool_result":
		return contentChars(block.Content)
	case "image":
		return imageCharEstimate
	case "thinking", "redacted_thinking":
		return 0
	default:
		return textChars(block.Text)
	}
}
