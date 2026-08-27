package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
)

// maxRequestBodyBytes bounds incoming request bodies (1M-context prompts with
// base64 images stay well below this).
const maxRequestBodyBytes = 128 << 20

const unsupportedAdvisorToolBeta = "advisor-tool-2026-03-01"

// handleMessages forwards Anthropic Messages API requests to Copilot, which
// serves the Anthropic wire format natively for Anthropic-vendor models. The
// proxy only rewrites the model name and enriches headers; bodies pass
// through untouched otherwise.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, errTypeInvalidRequest,
			"failed to read request body: "+err.Error())
		return
	}

	var payload map[string]json.RawMessage
	if parseErr := json.Unmarshal(body, &payload); parseErr != nil {
		writeAnthropicError(w, http.StatusBadRequest, errTypeInvalidRequest,
			"request body is not valid JSON")
		return
	}

	requested := rawString(payload["model"])
	if requested == "" {
		writeAnthropicError(w, http.StatusBadRequest, errTypeInvalidRequest, "model is required")
		return
	}

	resolution := s.catalog.Resolve(requested)
	if resolution.Known && !resolution.Model.SupportsAnthropicMessages() {
		writeAnthropicError(w, http.StatusBadRequest, errTypeInvalidRequest, fmt.Sprintf(
			"model %q does not support the Anthropic Messages API on Copilot; "+
				"this proxy serves Anthropic-vendor models only", resolution.ID))
		return
	}
	if resolution.ID != requested {
		s.logger.DebugContext(ctx, "model resolved", "requested", requested, "resolved", resolution.ID)
		encoded, encodeErr := json.Marshal(resolution.ID)
		if encodeErr != nil {
			writeAnthropicError(w, http.StatusInternalServerError, errTypeAPI,
				"failed to encode resolved model name")
			return
		}
		payload["model"] = encoded
	}

	insight := inspectMessages(payload["messages"])
	upstreamBody, err := json.Marshal(payload)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, errTypeAPI,
			"failed to re-encode request body")
		return
	}

	resp, err := s.copilot.Do(ctx, copilot.CallOptions{
		Method: http.MethodPost,
		Path:   "/v1/messages",
		Body:   upstreamBody,
		Header: s.messageHeaders(r, resolution, insight),
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "upstream messages request failed", "error", err)
		writeAnthropicError(w, http.StatusBadGateway, errTypeAPI,
			"failed to reach the Copilot API")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if isEventStream(resp.Header) {
		s.relaySSE(ctx, w, resp)
		return
	}
	copyResponse(w, resp)
}

// messageHeaders builds the /v1/messages specific headers layered on top of
// the standard Copilot set.
func (s *Server) messageHeaders(
	r *http.Request,
	resolution copilot.Resolution,
	insight messageInsight,
) http.Header {
	header := http.Header{}
	header.Set("Anthropic-Version", copilot.AnthropicVersion)
	// Join all anthropic-beta field lines: clients may legally send several
	// (e.g. the SDK's comma-joined line plus an ANTHROPIC_CUSTOM_HEADERS one),
	// and Get would silently drop all but the first.
	if betas := supportedAnthropicBetas(r.Header.Values("Anthropic-Beta")); betas != "" {
		header.Set("Anthropic-Beta", betas)
	}
	visionCapable := !resolution.Known || resolution.Model.Capabilities.Supports.Vision
	if insight.vision && visionCapable {
		header.Set("Copilot-Vision-Request", "true")
	}
	return header
}

func supportedAnthropicBetas(headerValues []string) string {
	var supported []string
	for _, headerValue := range headerValues {
		for _, beta := range strings.Split(headerValue, ",") {
			beta = strings.TrimSpace(beta)
			if beta != "" && beta != unsupportedAdvisorToolBeta {
				supported = append(supported, beta)
			}
		}
	}
	return strings.Join(supported, ",")
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

func rawString(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

// isHopByHopHeader reports whether a canonical header key is connection-scoped
// (RFC 9110 section 7.6.1) and must not be relayed to the client.
func isHopByHopHeader(key string) bool {
	switch key {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Proxy-Connection", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

// copyHeaders relays upstream response headers so rate-limit metadata
// (retry-after, anthropic-ratelimit-*) and request ids survive the proxy.
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		canonical := http.CanonicalHeaderKey(key)
		if isHopByHopHeader(canonical) {
			continue
		}
		dst[canonical] = values
	}
}

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	copyHeaders(w.Header(), resp.Header)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", contentTypeJSON)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
