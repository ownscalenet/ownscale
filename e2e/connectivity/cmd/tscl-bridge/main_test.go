package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestBridgeProcessHelper(t *testing.T) {
	if os.Getenv("OWNSCALE_BRIDGE_TEST_HELPER") != "1" {
		return
	}
	main()
	os.Exit(0)
}

func TestProcessLifecycle(t *testing.T) {
	for _, ending := range []string{"shutdown", "EOF", "SIGTERM", "SIGINT"} {
		t.Run(ending, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBridgeProcessHelper$")
			cmd.Env = append(os.Environ(), "OWNSCALE_BRIDGE_TEST_HELPER=1")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = input.Close()
				_ = output.Close()
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			encoder, decoder := json.NewEncoder(input), json.NewDecoder(output)
			exchange := func(op string) {
				t.Helper()
				if err := encoder.Encode(map[string]any{
					"jsonrpc": "2.0", "id": op, "method": op, "params": map[string]any{"timeout": "1000ms"},
				}); err != nil {
					t.Fatal(err)
				}
				var reply struct {
					ID     string          `json:"id"`
					Result json.RawMessage `json:"result"`
					Error  json.RawMessage `json:"error"`
				}
				if err := decoder.Decode(&reply); err != nil {
					t.Fatal(err)
				}
				if reply.ID != op || len(reply.Result) == 0 || len(reply.Error) > 0 {
					t.Fatalf("reply = %+v", reply)
				}
			}
			exchange("hello")
			switch ending {
			case "shutdown":
				exchange("shutdown")
			case "EOF":
				if err := input.Close(); err != nil {
					t.Fatal(err)
				}
			case "SIGTERM":
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			case "SIGINT":
				if err := cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			}
			if err := cmd.Wait(); err != nil {
				t.Fatalf("process failed: %v; stderr: %s", err, &stderr)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected diagnostics: %s", &stderr)
			}
		})
	}
}
