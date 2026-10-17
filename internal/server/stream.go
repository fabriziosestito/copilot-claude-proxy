package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// sseDoneData is the OpenAI stream terminator. Copilot's gateway appends it
// to every stream, including Anthropic ones where it is not part of the
// protocol, so each dialect decides whether to relay it.
const sseDoneData = "[DONE]"

// sseDialect captures how one wire protocol ends a stream and reports a
// mid-stream failure.
type sseDialect struct {
	// keepDone relays the gateway's [DONE] sentinel instead of dropping it.
	keepDone bool
	// errorEvent renders a terminal error event for the dialect.
	errorEvent func(message string) []byte
}

// anthropicSSE drops [DONE] and emits an Anthropic error event.
func anthropicSSE() sseDialect {
	return sseDialect{
		keepDone: false,
		errorEvent: func(message string) []byte {
			payload, err := json.Marshal(newAnthropicError(errTypeAPI, message))
			if err != nil {
				return nil
			}
			return fmt.Appendf(nil, "event: error\ndata: %s\n\n", payload)
		},
	}
}

// openAISSE keeps [DONE], which OpenAI clients wait for, and emits the error
// as a data event in the OpenAI error shape.
func openAISSE() sseDialect {
	return sseDialect{
		keepDone: true,
		errorEvent: func(message string) []byte {
			payload, err := json.Marshal(newOpenAIError(errTypeAPI, message, ""))
			if err != nil {
				return nil
			}
			return fmt.Appendf(nil, "data: %s\n\n", payload)
		},
	}
}

func isEventStream(header http.Header) bool {
	return strings.HasPrefix(header.Get("Content-Type"), "text/event-stream")
}

// relaySSE forwards the upstream SSE stream verbatim, flushing at event
// boundaries. When the upstream breaks mid-stream a terminal error event in
// the dialect's shape is emitted so clients fail fast instead of hanging.
func (s *Server) relaySSE(ctx context.Context, w http.ResponseWriter, resp *http.Response, dialect sseDialect) {
	header := w.Header()
	copyHeaders(header, resp.Header)
	// The relayed body may differ from upstream ([DONE] stripped, error event
	// appended), so a stale length must not be forwarded.
	header.Del("Content-Length")
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)

	flusher, canFlush := w.(http.Flusher)
	flush := func() {
		if canFlush {
			flusher.Flush()
		}
	}

	s.relayEvents(ctx, w, bufio.NewReader(resp.Body), flush, dialect)
	flush()
}

// relayEvents copies SSE lines from reader to w until the stream ends, the
// gateway's [DONE] sentinel appears, or the client goes away. The sentinel
// always ends the relay: a dialect that keeps it gets the line plus its blank
// terminator, and whatever the gateway does afterwards (a late reset is
// common) is not an error the client needs to hear about.
func (s *Server) relayEvents(
	ctx context.Context,
	w http.ResponseWriter,
	reader *bufio.Reader,
	flush func(),
	dialect sseDialect,
) {
	atEventBoundary := true
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			done, boundary := s.relayLine(ctx, w, line, flush, dialect)
			if done {
				return
			}
			atEventBoundary = boundary
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				s.activity.fail("stream aborted")
				s.logger.WarnContext(ctx, "upstream stream aborted", "error", err)
				writeStreamError(w, atEventBoundary, dialect, "upstream stream aborted unexpectedly")
			}
			return
		}
	}
}

// relayLine forwards one SSE line. The first result reports whether the relay
// is finished (sentinel reached or client gone); the second whether the line
// left the stream at an event boundary.
func (s *Server) relayLine(
	ctx context.Context,
	w http.ResponseWriter,
	line string,
	flush func(),
	dialect sseDialect,
) (bool, bool) {
	if isDoneSentinel(line) {
		if dialect.keepDone {
			_, _ = io.WriteString(w, "data: "+sseDoneData+"\n\n")
		}
		return true, true
	}
	if _, writeErr := io.WriteString(w, line); writeErr != nil {
		s.logger.DebugContext(ctx, "client disconnected during stream", "error", writeErr)
		return true, false
	}
	boundary := isEventBoundary(line)
	if boundary {
		flush()
	}
	return false, strings.HasSuffix(line, "\n") && boundary
}

// writeStreamError emits a terminal error event on the stream. When the
// upstream died mid-line or mid-event, the dangling fragment is closed off
// first so the error event is dispatched as a clean event of its own instead
// of being merged into the truncated one.
func writeStreamError(w io.Writer, atEventBoundary bool, dialect sseDialect, message string) {
	event := dialect.errorEvent(message)
	if event == nil {
		return
	}
	if !atEventBoundary {
		_, _ = io.WriteString(w, "\n\n")
	}
	_, _ = w.Write(event)
}

// isEventBoundary reports whether the line is the blank separator between SSE events.
func isEventBoundary(line string) bool {
	return strings.TrimRight(line, "\r\n") == ""
}

func isDoneSentinel(line string) bool {
	data, found := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data:")
	return found && strings.TrimSpace(data) == sseDoneData
}
