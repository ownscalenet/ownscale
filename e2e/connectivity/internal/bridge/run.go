package bridge

import (
	"context"
	"errors"
	"io"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/ownscalenet/ownscale/derrors"
)

// Run serves JSON-RPC requests over newline-delimited JSON frames on
// input, dispatching them to a fresh dispatcher, until the client closes
// its side of the pipe, calls "shutdown" and then closes it, or ctx is
// cancelled. It owns input and closes it to interrupt a blocked read
// before returning.
func Run(ctx context.Context, input io.ReadCloser, output io.Writer) (err error) {
	defer derrors.Wrap(&err, "Run()")
	return run(ctx, input, output, newDispatcher())
}

func run(parent context.Context, input io.ReadCloser, output io.Writer, d *dispatcher) (err error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Closing input unblocks the channel's blocked read so the server can
	// notice cancellation even if the client never writes or closes on its
	// own (e.g. it hung or was killed).
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		<-ctx.Done()
		_ = input.Close()
	}()

	// The framing library doesn't cancel a handler's context just because
	// the underlying read failed or hit EOF (a handler blocked on tsnet
	// work, e.g. a slow "create", has no other way to learn the client
	// disconnected). Propagate that as cancellation of ctx so in-flight
	// handlers unblock the same way they do for an explicit parent cancel.
	// The same applies to a broken output stream: jrpc2 silently swallows a
	// failed response Send and keeps waiting for more input, so a write
	// failure must also trigger cancellation or the server would hang
	// forever with a dead output pipe.
	reader := &cancelOnError{ReadCloser: input, cancel: cancel}
	base, ok := output.(io.WriteCloser)
	if !ok {
		base = nopCloser{output}
	}
	writer := &cancelOnErrorWriter{WriteCloser: base, cancel: cancel, afterWrite: func() {
		// The shutdown reply has just been flushed to the client; stop
		// accepting further work ourselves rather than waiting for the
		// client to also close its input side (it may not, e.g. if it's
		// pipelining shutdown with other teardown that never reaches us).
		if d.shutdownRequested.Load() {
			cancel()
		}
	}}

	srv := jrpc2.NewServer(d, &jrpc2.ServerOptions{
		Concurrency: 1, // preserve one-request-in-flight semantics
		NewContext:  func() context.Context { return ctx },
	})
	srv.Start(channel.Line(reader, writer))
	srvErr := srv.Wait()

	cancel()
	<-watchDone
	if srvErr != nil && (parent.Err() != nil || d.shutdownRequested.Load()) {
		// The read/write failure was a side effect of us closing input in
		// response to the caller cancelling ctx, or of our own post-shutdown
		// self-stop above, not a real bridge failure.
		srvErr = nil
	}
	return errors.Join(srvErr, d.close())
}

// cancelOnError calls cancel the first time a Read on the underlying stream
// fails (EOF or otherwise), so blocked in-flight handlers observe client
// disconnection as context cancellation.
type cancelOnError struct {
	io.ReadCloser
	cancel  context.CancelFunc
	stopped bool
}

func (r *cancelOnError) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err != nil && !r.stopped {
		r.stopped = true
		r.cancel()
	}
	return n, err
}

// cancelOnErrorWriter is cancelOnError's write-side counterpart: a broken
// output stream is otherwise invisible to the server (see run's comment).
// afterWrite, if set, runs after every successful write (used to trigger a
// self-stop once the shutdown reply has been flushed).
type cancelOnErrorWriter struct {
	io.WriteCloser
	cancel     context.CancelFunc
	afterWrite func()
	stopped    bool
}

func (w *cancelOnErrorWriter) Write(p []byte) (int, error) {
	n, err := w.WriteCloser.Write(p)
	if err != nil {
		if !w.stopped {
			w.stopped = true
			w.cancel()
		}
		return n, err
	}
	if w.afterWrite != nil {
		w.afterWrite()
	}
	return n, err
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
