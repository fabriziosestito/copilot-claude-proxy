package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
)

// anthropicDialect forwards Anthropic Messages API requests to Copilot, which
// serves the Anthropic wire format natively for Anthropic-vendor models.
func anthropicDialect() wireDialect {
	return wireDialect{
		upstreamPath: "/v1/messages",
		writeError: func(w http.ResponseWriter, status int, errType, message, _ string) {
			writeAnthropicError(w, status, errType, message)
		},
		rejectModel: func(model copilot.Model) string {
			if model.SupportsAnthropicMessages() {
				return ""
			}
			return fmt.Sprintf("model %q does not support the Anthropic Messages API on Copilot", model.ID)
		},
		hasImage: func(payload map[string]json.RawMessage) bool {
			return inspectMessages(payload["messages"]).vision
		},
		headers: messageHeaders,
		sse:     anthropicSSE(),
	}
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	s.forward(w, r, anthropicDialect())
}

// messageHeaders builds the /v1/messages specific headers layered on top of
// the standard Copilot set.
func messageHeaders(r *http.Request, resolution copilot.Resolution, vision bool) http.Header {
	header := http.Header{}
	header.Set("Anthropic-Version", copilot.AnthropicVersion)
	// Join all anthropic-beta field lines: clients may legally send several
	// (e.g. the SDK's comma-joined line plus an ANTHROPIC_CUSTOM_HEADERS one),
	// and Get would silently drop all but the first.
	if betas := r.Header.Values("Anthropic-Beta"); len(betas) > 0 {
		header.Set("Anthropic-Beta", strings.Join(betas, ","))
	}
	visionCapable := !resolution.Known || resolution.Model.Capabilities.Supports.Vision
	if vision && visionCapable {
		header.Set("Copilot-Vision-Request", "true")
	}
	return header
}

// messageInsight summarizes request traits that influence upstream headers.
type messageInsight struct {
	// vision is true when any message carries an image block.
	vision bool
}

type inspectedMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type inspectedBlock struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
}

func inspectMessages(raw json.RawMessage) messageInsight {
	var messages []inspectedMessage
	if len(raw) == 0 || json.Unmarshal(raw, &messages) != nil {
		return messageInsight{}
	}
	var insight messageInsight
	for _, message := range messages {
		if contentHasImage(message.Content, true) {
			insight.vision = true
			break
		}
	}
	return insight
}

// contentHasImage detects image blocks, including images nested one level
// inside tool_result blocks (e.g. screenshots returned by tools).
func contentHasImage(content json.RawMessage, recurse bool) bool {
	var blocks []inspectedBlock
	if len(content) == 0 || json.Unmarshal(content, &blocks) != nil {
		return false
	}
	for _, block := range blocks {
		if block.Type == "image" {
			return true
		}
		if recurse && block.Type == "tool_result" && contentHasImage(block.Content, false) {
			return true
		}
	}
	return false
}
