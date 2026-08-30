package dockerctl

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/ownscalenet/ownscale/derrors"
)

type InvalidConfigError struct{ msg string }

func NewInvalidConfigError(msg string) *InvalidConfigError {
	return &InvalidConfigError{msg: msg}
}

func (e *InvalidConfigError) Error() string { return e.msg }

// ConfigTest execs `headscale configtest` inside subdomain's headscale
// container against its current, on-disk config.yaml.
// It returns nil if headscale accepts the config, or an *InvalidConfigError
// carrying its output if not.
func (c *Ctl) ConfigTest(ctx context.Context, subdomain string) (err error) {
	defer derrors.Wrap(&err, "c.ConfigTest(%q)", subdomain)

	containerName := HSContainerName(subdomain)

	execResp, err := c.cli.ContainerExecCreate(ctx, containerName, container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"headscale", "configtest"},
	})
	if err != nil {
		return err
	}

	// Tty is deliberately false: see MintHeadscaleAPIKey for why (some
	// Docker Engine versions frame the attach stream with stdcopy's
	// multiplexing headers even with Tty:true, corrupting output).
	attachResp, err := c.cli.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return err
	}
	var stdout, stderr bytes.Buffer
	_, readErr := stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
	attachResp.Close()
	if readErr != nil {
		return readErr
	}

	inspectResp, err := c.cli.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return err
	}
	if inspectResp.ExitCode != 0 {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = fmt.Sprintf("headscale configtest exited with code %d", inspectResp.ExitCode)
		}
		return &InvalidConfigError{msg: msg}
	}
	return nil
}
