package server

import "net/http"

// modelCreatedAt is a fixed timestamp: Copilot's catalog carries no creation
// dates, but the Anthropic wire format requires one.
const modelCreatedAt = "2025-01-01T00:00:00Z"

type anthropicModelInfo struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

type anthropicModelList struct {
	Data    []anthropicModelInfo `json:"data"`
	HasMore bool                 `json:"has_more"`
	FirstID string               `json:"first_id,omitempty"`
	LastID  string               `json:"last_id,omitempty"`
}

// handleModels serves GET /v1/models, which both wire formats claim. The
// Anthropic SDK and Claude Code always send Anthropic-Version, so that header
// selects the Anthropic list; every other client gets the OpenAI list.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Anthropic-Version") != "" {
		s.handleAnthropicModels(w, r)
		return
	}
	s.handleOpenAIModels(w, r)
}

// handleAnthropicModels lists the catalog models usable through the Anthropic
// Messages API in the Anthropic list format.
func (s *Server) handleAnthropicModels(w http.ResponseWriter, _ *http.Request) {
	models := s.catalog.Models()
	data := make([]anthropicModelInfo, 0, len(models))
	for _, model := range models {
		if !model.SupportsAnthropicMessages() {
			continue
		}
		displayName := model.Name
		if displayName == "" {
			displayName = model.ID
		}
		data = append(data, anthropicModelInfo{
			Type:        "model",
			ID:          model.ID,
			DisplayName: displayName,
			CreatedAt:   modelCreatedAt,
		})
	}

	list := anthropicModelList{Data: data}
	if len(data) > 0 {
		list.FirstID = data[0].ID
		list.LastID = data[len(data)-1].ID
	}
	writeJSON(w, http.StatusOK, list)
}
