package dockerctl

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/ownscalenet/ownscale/internal/derrors"
)

// streamLogs follows containerID's stdout/stderr (as "docker logs -f"
// would) and re-emits each line through c.logger, tagged with name and the
// originating stream. It's meant to be run in its own goroutine
// (go c.streamLogs(id, name)); it never returns an error to the caller,
// since a container's own logs are best-effort observability, not
// something that should fail the operation that started it. Streaming
// stops when c.ctx is canceled (see Close) or the container itself stops
// producing logs (e.g. it exits).
func (c *Ctl) streamLogs(containerID, name string) {
	rc, err := c.cli.ContainerLogs(c.ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		// Tail 0 means no history.
		Tail: "0",
	})
	if err != nil {
		if c.ctx.Err() == nil {
			c.logger.Warn("stream container logs: attach failed", "container", name, "error", err)
		}
		return
	}
	defer rc.Close()

	stdout := &lineWriter{logger: c.logger, attrs: []any{"container", name, "stream", "stdout"}}
	stderr := &lineWriter{logger: c.logger, attrs: []any{"container", name, "stream", "stderr"}}
	if _, err := stdcopy.StdCopy(stdout, stderr, rc); err != nil && !errors.Is(err, context.Canceled) && c.ctx.Err() == nil {
		c.logger.Debug("container log stream ended", "container", name, "error", err)
	}
}

// StreamAllLogs starts streaming logs for every currently running organization
// container. It's meant to be called once at ownscale startup so containers
// that were already running are logged too.
func (c *Ctl) StreamAllLogs(ctx context.Context) (err error) {
	defer derrors.Wrap(&err, "c.StreamAllLogs()")

	f := filters.NewArgs(
		filters.Arg("label", "ownscale.organization=true"),
		filters.Arg("status", "running"),
	)
	containers, err := c.cli.ContainerList(ctx, container.ListOptions{Filters: f})
	if err != nil {
		return err
	}
	for _, ct := range containers {
		name := ct.ID
		if len(ct.Names) > 0 {
			name = ct.Names[0]
		}
		go c.streamLogs(ct.ID, name)
	}
	return nil
}

// lineWriter is an io.Writer that buffers partial writes and logs one
// record per complete line (delimited by '\n', with a trailing '\r'
// trimmed for "\r\n" line endings), so multi-line container output isn't
// split across log records at arbitrary Write boundaries.
type lineWriter struct {
	logger *slog.Logger
	attrs  []any
	buf    bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadString('\n')
		if err != nil {
			// No complete line yet: put the partial data back and wait
			// for more.
			w.buf.Reset()
			w.buf.WriteString(line)
			break
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		w.logger.Info(line, w.attrs...)
	}
	return len(p), nil
}
