package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/fabriziosestito/copilot-claude-proxy/internal/copilot"
)

// maxRequestBodyBytes bounds incoming request bodies (1M-context prompts with
// base64 images stay well below this).
const maxRequestBodyBytes = 128 << 20

// wireDialect captures everything that differs between the Anthropic and
// OpenAI passthrough routes: how errors are shaped, which models the catalog
// admits, which headers enrich the upstream call, and how streams end.
type wireDialect struct {
	// upstreamPath is the Copilot path requests are forwarded to.
	upstreamPath string
	// writeError answers the client in the dialect's error format. param
	// names the offending request field when the dialect reports one.
	writeError func(w http.ResponseWriter, status int, errType, message, param string)
	// rejectModel returns a non-empty message when the catalog says the
	// resolved model cannot be served on this route.
	rejectModel func(model copilot.Model) string
	// hasImage reports whether the request body carries an image.
	hasImage func(payload map[string]json.RawMessage) bool
	// headers builds the route-specific headers layered on top of the
	// standard Copilot set.
	headers func(r *http.Request, resolution copilot.Resolution, vision bool) http.Header
	// sse describes how the dialect terminates and fails a stream.
	sse sseDialect
}

// forward relays one request to Copilot. The proxy only rewrites the model
// name and enriches headers; bodies pass through untouched otherwise.
func (s *Server) forward(w http.ResponseWriter, r *http.Request, dialect wireDialect) {
	ctx := r.Context()

	payload, requested, ok := readRequest(w, r, dialect)
	if !ok {
		return
	}

	resolution := s.catalog.Resolve(requested)
	if resolution.Known {
		if message := dialect.rejectModel(resolution.Model); message != "" {
			dialect.writeError(w, http.StatusBadRequest, errTypeInvalidRequest, message, "model")
			return
		}
	}
	if resolution.ID != requested {
		s.logger.DebugContext(ctx, "model resolved", "requested", requested, "resolved", resolution.ID)
		encoded, encodeErr := json.Marshal(resolution.ID)
		if encodeErr != nil {
			dialect.writeError(w, http.StatusInternalServerError, errTypeAPI,
				"failed to encode resolved model name", "")
			return
		}
		payload["model"] = encoded
	}

	vision := dialect.hasImage(payload)
	upstreamBody, err := json.Marshal(payload)
	if err != nil {
		dialect.writeError(w, http.StatusInternalServerError, errTypeAPI, "failed to re-encode request body", "")
		return
	}

	s.activity.record(requested, resolution.ID)
	resp, err := s.copilot.Do(ctx, copilot.CallOptions{
		Method: http.MethodPost,
		Path:   dialect.upstreamPath,
		Body:   upstreamBody,
		Header: dialect.headers(r, resolution, vision),
	})
	if err != nil {
		// A client that canceled its request also fails the upstream call;
		// only a failure with the request still live is an upstream problem,
		// mirroring the stream path.
		if ctx.Err() == nil {
			s.activity.fail("upstream unreachable")
		}
		s.logger.ErrorContext(ctx, "upstream request failed", "path", dialect.upstreamPath, "error", err)
		dialect.writeError(w, http.StatusBadGateway, errTypeAPI, "failed to reach the Copilot API", "")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		s.activity.fail("upstream " + strconv.Itoa(resp.StatusCode))
	}

	if isEventStream(resp.Header) {
		s.relaySSE(ctx, w, resp, dialect.sse)
		return
	}
	copyResponse(w, resp)
}

// readRequest decodes the request body and extracts the model name, answering
// the client in the dialect's error shape when either fails.
func readRequest(
	w http.ResponseWriter,
	r *http.Request,
	dialect wireDialect,
) (map[string]json.RawMessage, string, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	if err != nil {
		dialect.writeError(w, http.StatusBadRequest, errTypeInvalidRequest,
			"failed to read request body: "+err.Error(), "")
		return nil, "", false
	}

	var payload map[string]json.RawMessage
	if parseErr := json.Unmarshal(body, &payload); parseErr != nil {
		dialect.writeError(w, http.StatusBadRequest, errTypeInvalidRequest, "request body is not valid JSON", "")
		return nil, "", false
	}

	requested := rawString(payload["model"])
	if requested == "" {
		dialect.writeError(w, http.StatusBadRequest, errTypeInvalidRequest, "model is required", "model")
		return nil, "", false
	}
	return payload, requested, true
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
