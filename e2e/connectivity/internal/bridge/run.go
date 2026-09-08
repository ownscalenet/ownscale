package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/ownscalenet/ownscale/derrors"
)

// Run serves serialized JSON-lines commands until shutdown, EOF, or cancellation.
// It owns input and closes it to interrupt a blocked read before returning.
func Run(ctx context.Context, input io.ReadCloser, output io.Writer) (err error) {
	defer derrors.Wrap(&err, "Run()")
	return run(ctx, input, output, newDispatcher())
}

func run(parent context.Context, input io.ReadCloser, output io.Writer, d *dispatcher) (err error) {
	ctx, cancel := context.WithCancel(parent)
	frames := make(chan []byte)
	readDone := make(chan struct{})
	var readErr error
	go func() {
		defer close(readDone)
		defer cancel()
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), maxFrameBytes+1)
		scanner.Split(splitFrame)
		for scanner.Scan() {
			frame := append([]byte(nil), scanner.Bytes()...)
			if len(frame) > maxFrameBytes {
				readErr = failure("invalid_command")
				return
			}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
		}
		if scanner.Err() != nil && ctx.Err() == nil {
			readErr = failure("invalid_command")
		}
	}()
	defer func() {
		cancel()
		_ = input.Close()
		<-readDone
		err = errors.Join(err, readErr, d.close())
	}()
	for {
		select {
		case <-ctx.Done():
			_ = input.Close()
			<-readDone
			if readErr != nil {
				if writeErr := writeResponse(output, response{Version: version, ID: "", Error: classify(readErr)}); writeErr != nil {
					return writeErr
				}
			}
			return nil
		case frame := <-frames:
			var req request
			decodeErr := decode(frame, &req)
			reply := response{Version: version, ID: ""}
			if handlePattern.MatchString(req.ID) {
				reply.ID = req.ID
			}
			var dispatchErr error
			if decodeErr != nil {
				dispatchErr = decodeErr
			} else {
				reply.Result, dispatchErr = d.dispatch(ctx, req)
			}
			if dispatchErr != nil {
				reply.Error = classify(dispatchErr)
			}
			if err := writeResponse(output, reply); err != nil {
				return err
			}
			if req.Op == "shutdown" && decodeErr == nil && (dispatchErr == nil || reply.Error.Code == "bridge_failure") {
				return nil
			}
		}
	}
}

func splitFrame(data []byte, atEOF bool) (int, []byte, error) {
	if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
		return newline + 1, data[:newline], nil
	}
	if atEOF && len(data) > 0 {
		return 0, nil, failure("invalid_command")
	}
	return 0, nil, nil
}

func writeResponse(output io.Writer, reply response) error {
	body, err := json.Marshal(reply)
	if err != nil || len(body) > maxFrameBytes {
		body, _ = json.Marshal(response{Version: version, ID: reply.ID, Error: failure("bridge_failure")})
	}
	body = append(body, '\n')
	n, err := output.Write(body)
	if err != nil || n != len(body) {
		return failure("bridge_failure")
	}
	return nil
}
