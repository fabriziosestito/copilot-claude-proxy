package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// signalHandling wires SIGINT and SIGTERM to a command context and lets the
// `run` command hand Ctrl-C over to its foreground Claude Code child.
//
// SIGINT is delivered to two channels: interrupt, which cancels the context
// and can be detached, and sink, which is never unregistered. The sink exists
// so a Go signal handler stays installed for SIGINT after detaching; the last
// [signal.Stop] for a signal restores the default disposition, and the first
// Ctrl-C inside Claude Code would then kill the proxy out from under it.
// Keeping the signal handled in Go rather than setting SIG_IGN also matters,
// because an ignored disposition survives exec and would leave the child deaf
// to Ctrl-C as well.
type signalHandling struct {
	interrupt chan os.Signal
	terminate chan os.Signal
	sink      chan os.Signal
}

// notifySignals returns a context canceled by SIGINT or SIGTERM, the handling
// handle used to detach SIGINT, and a cleanup func releasing everything.
func notifySignals(parent context.Context) (context.Context, *signalHandling, func()) {
	ctx, cancel := context.WithCancel(parent)
	handling := &signalHandling{
		interrupt: make(chan os.Signal, 1),
		terminate: make(chan os.Signal, 1),
		sink:      make(chan os.Signal, 1),
	}

	signal.Notify(handling.sink, os.Interrupt)
	signal.Notify(handling.interrupt, os.Interrupt)
	signal.Notify(handling.terminate, syscall.SIGTERM)

	go func() {
		defer cancel()
		for {
			select {
			case <-handling.interrupt:
				return
			case <-handling.terminate:
				return
			case <-handling.sink:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ctx, handling, func() {
		signal.Stop(handling.interrupt)
		signal.Stop(handling.terminate)
		signal.Stop(handling.sink)
		cancel()
	}
}

// DetachInterrupt stops SIGINT from canceling the command context, leaving it
// to the foreground child process; SIGTERM keeps canceling. The returned func
// restores the original behavior.
func (s *signalHandling) DetachInterrupt() func() {
	signal.Stop(s.interrupt)
	return func() { signal.Notify(s.interrupt, os.Interrupt) }
}
