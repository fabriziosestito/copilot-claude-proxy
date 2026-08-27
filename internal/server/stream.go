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

// sseDoneData is the OpenAI-style terminator Copilot's gateway appends even to
// Anthropic streams; it is not part of the Anthropic protocol and is dropped.
const sseDoneData = "[DONE]"

func isEventStream(header http.Header) bool {
	return strings.HasPrefix(header.Get("Content-Type"), "text/event-stream")
}

// relaySSE forwards the upstream Anthropic SSE stream verbatim, flushing at
// event boundaries. When the upstream breaks mid-stream a terminal Anthropic
// error event is emitted so clients fail fast instead of hanging.
func (s *Server) relaySSE(ctx context.Context, w http.ResponseWriter, resp *http.Response) {
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

	s.relayEvents(ctx, w, bufio.NewReader(resp.Body), flush)
	flush()
}

// relayEvents copies SSE lines from reader to w until the stream ends, the
// gateway's [DONE] sentinel appears, or the client goes away.
func (s *Server) relayEvents(
	ctx context.Context,
	w http.ResponseWriter,
	reader *bufio.Reader,
	flush func(),
) {
	atEventBoundary := true
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			if isDoneSentinel(line) {
				return
			}
			if _, writeErr := io.WriteString(w, line); writeErr != nil {
				s.logger.DebugContext(ctx, "client disconnected during stream", "error", writeErr)
				return
			}
			atEventBoundary = strings.HasSuffix(line, "\n") && isEventBoundary(line)
			if isEventBoundary(line) {
				flush()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				s.activity.fail("stream aborted")
				s.logger.WarnContext(ctx, "upstream stream aborted", "error", err)
				writeStreamError(w, atEventBoundary, "upstream stream aborted unexpectedly")
			}
			return
		}
	}
}

// writeStreamError emits a terminal Anthropic error event on the stream. When
// the upstream died mid-line or mid-event, the dangling fragment is closed
// off first so the error event is dispatched as a clean event of its own
// instead of being merged into the truncated one.
func writeStreamError(w io.Writer, atEventBoundary bool, message string) {
	payload, err := json.Marshal(newAnthropicError(errTypeAPI, message))
	if err != nil {
		return
	}
	if !atEventBoundary {
		_, _ = io.WriteString(w, "\n\n")
	}
	_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload)
}

// isEventBoundary reports whether the line is the blank separator between SSE events.
func isEventBoundary(line string) bool {
	return strings.TrimRight(line, "\r\n") == ""
}

func isDoneSentinel(line string) bool {
	data, found := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "data:")
	return found && strings.TrimSpace(data) == sseDoneData
}
