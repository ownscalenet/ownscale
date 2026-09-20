package dockerctl

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/ownscalenet/ownscale/dockerctl/dockerfake"
)

func TestConfigTest(t *testing.T) {
	tests := []struct {
		name       string
		exitCode   int
		stderr     string
		wantErr    bool
		wantErrMsg string
	}{
		{name: "valid config", exitCode: 0, wantErr: false},
		{name: "invalid config", exitCode: 1, stderr: "dns.base_domain must be set when using MagicDNS", wantErr: true, wantErrMsg: "dns.base_domain must be set when using MagicDNS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctl, _, _ := newProvisionedTestCtl(t)
			api := ctl.cli.(*dockerfake.API)
			api.ContainerExecInspectFn = func(ctx context.Context, execID string) (container.ExecInspect, error) {
				return container.ExecInspect{ExitCode: tt.exitCode}, nil
			}
			api.ContainerExecAttachFn = func(ctx context.Context, execID string, options container.ExecAttachOptions) (types.HijackedResponse, error) {
				clientConn, serverConn := net.Pipe()
				go func() {
					if tt.stderr != "" {
						w := stdcopy.NewStdWriter(serverConn, stdcopy.Stderr)
						_, _ = w.Write([]byte(tt.stderr))
					}
					_ = serverConn.Close()
				}()
				return types.NewHijackedResponse(clientConn, ""), nil
			}

			err := ctl.ConfigTest(t.Context(), "acme")
			if tt.wantErr {
				if err == nil {
					t.Fatal("ConfigTest: want error, got nil")
				}
				var invalid *InvalidConfigError
				if !errors.As(err, &invalid) {
					t.Fatalf("ConfigTest error = %v, want *InvalidConfigError", err)
				}
				if invalid.Error() != tt.wantErrMsg {
					t.Errorf("ConfigTest error message = %q, want %q", invalid.Error(), tt.wantErrMsg)
				}
			} else if err != nil {
				t.Fatalf("ConfigTest: %v", err)
			}
		})
	}
}
