package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
)

// openAIDialect forwards one OpenAI-dialect API to Copilot. Both the
// Responses and the Chat Completions routes share this shape; they differ
// only in the upstream path and the name used in error messages.
func openAIDialect(upstreamPath, apiName string) wireDialect {
	return wireDialect{
		upstreamPath: upstreamPath,
		writeError:   writeOpenAIError,
		rejectModel: func(model copilot.Model) string {
			if model.SupportsEndpoint(upstreamPath) {
				return ""
			}
			return fmt.Sprintf("model %q does not support %s on Copilot", model.ID, apiName)
		},
		hasImage: openAIHasImage,
		headers:  openAIHeaders,
		sse:      openAISSE(),
	}
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	s.forward(w, r, openAIDialect(copilot.OpenAIResponsesEndpoint, "the OpenAI Responses API"))
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.forward(w, r, openAIDialect(copilot.OpenAIChatCompletionsEndpoint, "the OpenAI Chat Completions API"))
}

// openAIHeaders builds the OpenAI-route headers layered on top of the
// standard Copilot set.
func openAIHeaders(_ *http.Request, resolution copilot.Resolution, vision bool) http.Header {
	header := http.Header{}
	visionCapable := !resolution.Known || resolution.Model.Capabilities.Supports.Vision
	if vision && visionCapable {
		header.Set("Copilot-Vision-Request", "true")
	}
	return header
}

// openAIPart is the common shape of a content part in both OpenAI dialects:
// Chat Completions uses "image_url", the Responses API uses "input_image".
type openAIPart struct {
	Type string `json:"type"`
}

// openAIItem is a Chat Completions message or a Responses input item. Parts
// live under "content", or under "output" for a function_call_output item
// that returns a screenshot to the model.
type openAIItem struct {
	Content json.RawMessage `json:"content"`
	Output  json.RawMessage `json:"output"`
}

// openAIHasImage reports whether the request carries an image part, so the
// proxy can set Copilot's vision header. A string input or content is text.
func openAIHasImage(payload map[string]json.RawMessage) bool {
	for _, key := range []string{"messages", "input"} {
		if openAIItemsHaveImage(payload[key]) {
			return true
		}
	}
	return false
}

func openAIItemsHaveImage(raw json.RawMessage) bool {
	var items []openAIItem
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return false
	}
	for _, item := range items {
		if openAIPartsHaveImage(item.Content) || openAIPartsHaveImage(item.Output) {
			return true
		}
	}
	return false
}

func openAIPartsHaveImage(raw json.RawMessage) bool {
	var parts []openAIPart
	if len(raw) == 0 || json.Unmarshal(raw, &parts) != nil {
		return false
	}
	for _, part := range parts {
		if part.Type == "image_url" || part.Type == "input_image" {
			return true
		}
	}
	return false
}

// openAIModelCreated is the fixed creation time reported for every model, the
// same 2025-01-01 instant the Anthropic list uses, as a Unix timestamp.
const openAIModelCreated = 1735689600

type openAIModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type openAIModelList struct {
	Object string            `json:"object"`
	Data   []openAIModelInfo `json:"data"`
}

// handleOpenAIModels lists the catalog models reachable through an OpenAI
// dialect, in the OpenAI list format.
func (s *Server) handleOpenAIModels(w http.ResponseWriter, _ *http.Request) {
	models := s.catalog.Models()
	data := make([]openAIModelInfo, 0, len(models))
	for _, model := range models {
		if !model.SupportsOpenAI() {
			continue
		}
		data = append(data, openAIModelInfo{
			ID:      model.ID,
			Object:  "model",
			Created: openAIModelCreated,
			OwnedBy: model.Vendor,
		})
	}
	writeJSON(w, http.StatusOK, openAIModelList{Object: "list", Data: data})
}
