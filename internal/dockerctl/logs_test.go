package dockerctl

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/ownscalenet/ownscale/internal/dockerctl/dockerfake"
)

// logRecord mirrors the JSON shape slog.NewJSONHandler emits, just enough
// to assert on the message and the attrs streamLogs/lineWriter attach.
type logRecord struct {
	Msg       string `json:"msg"`
	Container string `json:"container"`
	Stream    string `json:"stream"`
}

func newCapturingLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// syncBuffer is a bytes.Buffer safe for concurrent writes, needed when
// multiple streamLogs goroutines (as started by StreamAllLogs) log through
// the same handler at once.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func parseLogRecords(t *testing.T, buf *bytes.Buffer) []logRecord {
	t.Helper()
	return parseLogRecordsFromString(t, buf.String())
}

func parseLogRecordsFromString(t *testing.T, content string) []logRecord {
	t.Helper()
	var records []logRecord
	for line := range strings.SplitSeq(strings.TrimSpace(content), "\n") {
		if line == "" {
			continue
		}
		var rec logRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal log line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func TestLineWriter(t *testing.T) {
	t.Run("buffers partial writes across calls", func(t *testing.T) {
		var buf bytes.Buffer
		w := &lineWriter{logger: newCapturingLogger(&buf), attrs: []any{"container", "c1", "stream", "stdout"}}

		_, _ = w.Write([]byte("hello "))
		_, _ = w.Write([]byte("world\n"))

		records := parseLogRecords(t, &buf)
		if len(records) != 1 || records[0].Msg != "hello world" {
			t.Fatalf("records = %+v, want a single %q record", records, "hello world")
		}
	})

	t.Run("logs multiple lines from a single write", func(t *testing.T) {
		var buf bytes.Buffer
		w := &lineWriter{logger: newCapturingLogger(&buf), attrs: []any{"container", "c1", "stream", "stdout"}}

		_, _ = w.Write([]byte("line1\nline2\nline3\n"))

		records := parseLogRecords(t, &buf)
		want := []string{"line1", "line2", "line3"}
		if len(records) != len(want) {
			t.Fatalf("records = %+v, want %d records", records, len(want))
		}
		for i, r := range records {
			if r.Msg != want[i] {
				t.Errorf("records[%d].Msg = %q, want %q", i, r.Msg, want[i])
			}
			if r.Container != "c1" || r.Stream != "stdout" {
				t.Errorf("records[%d] = %+v, want container=c1 stream=stdout", i, r)
			}
		}
	})

	t.Run("trims a trailing carriage return", func(t *testing.T) {
		var buf bytes.Buffer
		w := &lineWriter{logger: newCapturingLogger(&buf), attrs: nil}

		_, _ = w.Write([]byte("crlf line\r\n"))

		records := parseLogRecords(t, &buf)
		if len(records) != 1 || records[0].Msg != "crlf line" {
			t.Fatalf("records = %+v, want a single %q record", records, "crlf line")
		}
	})

	t.Run("does not log an incomplete trailing line", func(t *testing.T) {
		var buf bytes.Buffer
		w := &lineWriter{logger: newCapturingLogger(&buf), attrs: nil}

		_, _ = w.Write([]byte("no newline yet"))

		if buf.Len() != 0 {
			t.Fatalf("expected no log output before a newline, got %q", buf.String())
		}
	})
}

func TestStreamAllLogs(t *testing.T) {
	var listFilters filters.Args
	var listCalled bool

	var frames bytes.Buffer
	stdoutW := stdcopy.NewStdWriter(&frames, stdcopy.Stdout)
	_, _ = stdoutW.Write([]byte("booted\n"))

	api := &dockerfake.API{
		ContainerListFn: func(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
			listCalled = true
			listFilters = options.Filters
			return []container.Summary{
				{ID: "id-1", Names: []string{"/hs-acme"}},
				{ID: "id-2", Names: []string{"/hp-acme"}},
			}, nil
		},
		ContainerLogsFn: func(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(frames.Bytes())), nil
		},
	}

	buf := &syncBuffer{}
	ctl, err := NewWithClient(api, t.TempDir(), slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	if err := ctl.StreamAllLogs(t.Context()); err != nil {
		t.Fatalf("StreamAllLogs: %v", err)
	}

	if !listCalled {
		t.Fatal("expected ContainerList to be called")
	}
	if !listFilters.ExactMatch("label", "ownscale.organization=true") {
		t.Errorf("filters = %+v, want a label=ownscale.organization=true filter", listFilters)
	}
	if !listFilters.ExactMatch("status", "running") {
		t.Errorf("filters = %+v, want a status=running filter", listFilters)
	}

	// streamLogs is started in goroutines (one per container); poll briefly
	// for both to have logged their line, tagged with the right container
	// name (derived from ct.Names[0]).
	deadline := time.Now().Add(2 * time.Second)
	var records []logRecord
	for time.Now().Before(deadline) {
		records = parseLogRecordsFromString(t, buf.String())
		if len(records) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	wantContainers := map[string]bool{"/hs-acme": false, "/hp-acme": false}
	for _, r := range records {
		if _, ok := wantContainers[r.Container]; ok {
			wantContainers[r.Container] = true
		}
	}
	for name, seen := range wantContainers {
		if !seen {
			t.Errorf("expected a log record for container %q, got records=%+v", name, records)
		}
	}
}

func TestStreamLogs(t *testing.T) {
	var frames bytes.Buffer
	stdoutW := stdcopy.NewStdWriter(&frames, stdcopy.Stdout)
	_, _ = stdoutW.Write([]byte("stdout line 1\nstdout line 2\n"))
	stderrW := stdcopy.NewStdWriter(&frames, stdcopy.Stderr)
	_, _ = stderrW.Write([]byte("stderr line 1\n"))

	api := &dockerfake.API{
		ContainerLogsFn: func(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error) {
			if !options.ShowStdout || !options.ShowStderr || !options.Follow {
				t.Errorf("options = %+v, want ShowStdout/ShowStderr/Follow all true", options)
			}
			return io.NopCloser(bytes.NewReader(frames.Bytes())), nil
		},
	}

	var buf bytes.Buffer
	ctl, err := NewWithClient(api, t.TempDir(), newCapturingLogger(&buf))
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { _ = ctl.Close() })

	// Call synchronously (rather than via "go") since the fake reader
	// reaches EOF on its own, so streamLogs returns without needing a
	// goroutine here.
	ctl.streamLogs("container-id", "hs-acme")

	records := parseLogRecords(t, &buf)
	want := []logRecord{
		{Msg: "stdout line 1", Container: "hs-acme", Stream: "stdout"},
		{Msg: "stdout line 2", Container: "hs-acme", Stream: "stdout"},
		{Msg: "stderr line 1", Container: "hs-acme", Stream: "stderr"},
	}
	if len(records) != len(want) {
		t.Fatalf("records = %+v, want %+v", records, want)
	}
	for _, w := range want {
		var found bool
		if slices.Contains(records, w) {
			found = true
		}
		if !found {
			t.Errorf("records = %+v, want it to contain %+v", records, w)
		}
	}
}
