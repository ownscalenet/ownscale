// Package dockerfake provides a fake implementation of dockerctl.API for
// use in tests, so dockerctl.Ctl (and its callers, e.g. cmd/osa and
// internal/orgs) can be exercised without a real Docker daemon.
package dockerfake

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// API is a fake dockerctl.API. It embeds the Docker SDK's own client interfaces
// as nil values, so it only needs to implement the handful of methods Ctl
// actually calls.
type API struct {
	client.ContainerAPIClient
	client.NetworkAPIClient
	client.VolumeAPIClient

	ContainerCreateFn      func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStartFn       func(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerStopFn        func(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemoveFn      func(ctx context.Context, containerID string, options container.RemoveOptions) error
	ContainerInspectFn     func(ctx context.Context, containerID string) (container.InspectResponse, error)
	ContainerListFn        func(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	CopyToContainerFn      func(ctx context.Context, containerID, path string, content io.Reader, options container.CopyToContainerOptions) error
	CopyFromContainerFn    func(ctx context.Context, containerID, srcPath string) (io.ReadCloser, container.PathStat, error)
	ContainerLogsFn        func(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error)
	ContainerExecCreateFn  func(ctx context.Context, containerID string, options container.ExecOptions) (container.ExecCreateResponse, error)
	ContainerExecAttachFn  func(ctx context.Context, execID string, options container.ExecAttachOptions) (types.HijackedResponse, error)
	ContainerExecInspectFn func(ctx context.Context, execID string) (container.ExecInspect, error)

	NetworkInspectFn    func(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error)
	NetworkCreateFn     func(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error)
	NetworkConnectFn    func(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error
	NetworkDisconnectFn func(ctx context.Context, networkID, containerID string, force bool) error
	NetworkRemoveFn     func(ctx context.Context, networkID string) error

	VolumeCreateFn func(ctx context.Context, options volume.CreateOptions) (volume.Volume, error)
	VolumeRemoveFn func(ctx context.Context, volumeID string, force bool) error

	CloseFn func() error
}

// ContainerCreate fakes container.Config creation, returning ContainerCreateFn's
// result if set, or a synthetic "fake-<containerName>" ID otherwise.
func (a *API) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
	if a.ContainerCreateFn != nil {
		return a.ContainerCreateFn(ctx, config, hostConfig, networkingConfig, platform, containerName)
	}
	return container.CreateResponse{ID: "fake-" + containerName}, nil
}

// ContainerStart fakes starting a container.
func (a *API) ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error {
	if a.ContainerStartFn != nil {
		return a.ContainerStartFn(ctx, containerID, options)
	}
	return nil
}

// ContainerStop fakes stopping a container.
func (a *API) ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error {
	if a.ContainerStopFn != nil {
		return a.ContainerStopFn(ctx, containerID, options)
	}
	return nil
}

// ContainerRemove fakes removing a container.
func (a *API) ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error {
	if a.ContainerRemoveFn != nil {
		return a.ContainerRemoveFn(ctx, containerID, options)
	}
	return nil
}

// ContainerInspect fakes inspecting a container, defaulting to a "running" state.
func (a *API) ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error) {
	if a.ContainerInspectFn != nil {
		return a.ContainerInspectFn(ctx, containerID)
	}
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{State: &container.State{Status: "running"}},
	}, nil
}

// ContainerList fakes listing containers, defaulting to an empty list.
func (a *API) ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error) {
	if a.ContainerListFn != nil {
		return a.ContainerListFn(ctx, options)
	}
	return nil, nil
}

// CopyToContainer fakes copying an archive into a container.
func (a *API) CopyToContainer(ctx context.Context, containerID, path string, content io.Reader, options container.CopyToContainerOptions) error {
	if a.CopyToContainerFn != nil {
		return a.CopyToContainerFn(ctx, containerID, path, content, options)
	}
	return nil
}

// CopyFromContainer fakes reading a file/archive out of a container,
// defaulting to "not found" since most tests don't need this path
// populated (e.g. Caddy's local CA is absent by default).
func (a *API) CopyFromContainer(ctx context.Context, containerID, srcPath string) (io.ReadCloser, container.PathStat, error) {
	if a.CopyFromContainerFn != nil {
		return a.CopyFromContainerFn(ctx, containerID, srcPath)
	}
	return nil, container.PathStat{}, os.ErrNotExist
}

// ContainerLogs fakes streaming a container's logs, defaulting to an empty
// (already at EOF) stream.
func (a *API) ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error) {
	if a.ContainerLogsFn != nil {
		return a.ContainerLogsFn(ctx, containerID, options)
	}
	return io.NopCloser(new(bytes.Buffer)), nil
}

// ContainerExecCreate fakes creating a container exec instance.
func (a *API) ContainerExecCreate(ctx context.Context, containerID string, options container.ExecOptions) (container.ExecCreateResponse, error) {
	if a.ContainerExecCreateFn != nil {
		return a.ContainerExecCreateFn(ctx, containerID, options)
	}
	return container.ExecCreateResponse{ID: "exec-" + containerID}, nil
}

// ContainerExecAttach fakes attaching to a container exec stream.
func (a *API) ContainerExecAttach(ctx context.Context, execID string, options container.ExecAttachOptions) (types.HijackedResponse, error) {
	if a.ContainerExecAttachFn != nil {
		return a.ContainerExecAttachFn(ctx, execID, options)
	}
	clientConn, serverConn := net.Pipe()
	go func() {
		w := stdcopy.NewStdWriter(serverConn, stdcopy.Stdout)
		_, _ = w.Write([]byte("fake-headscale-api-key\n"))
		_ = serverConn.Close()
	}()
	return types.NewHijackedResponse(clientConn, ""), nil
}

// ContainerExecInspect fakes inspecting a container exec instance.
func (a *API) ContainerExecInspect(ctx context.Context, execID string) (container.ExecInspect, error) {
	if a.ContainerExecInspectFn != nil {
		return a.ContainerExecInspectFn(ctx, execID)
	}
	return container.ExecInspect{ExitCode: 0}, nil
}

// NetworkInspect fakes inspecting a network, defaulting to "already exists".
func (a *API) NetworkInspect(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error) {
	if a.NetworkInspectFn != nil {
		return a.NetworkInspectFn(ctx, networkID, options)
	}
	return network.Inspect{}, nil
}

// NetworkCreate fakes creating a network.
func (a *API) NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
	if a.NetworkCreateFn != nil {
		return a.NetworkCreateFn(ctx, name, options)
	}
	return network.CreateResponse{}, nil
}

// NetworkConnect fakes connecting a container to a network.
func (a *API) NetworkConnect(ctx context.Context, networkID, containerID string, config *network.EndpointSettings) error {
	if a.NetworkConnectFn != nil {
		return a.NetworkConnectFn(ctx, networkID, containerID, config)
	}
	return nil
}

// NetworkDisconnect fakes disconnecting a container from a network.
func (a *API) NetworkDisconnect(ctx context.Context, networkID, containerID string, force bool) error {
	if a.NetworkDisconnectFn != nil {
		return a.NetworkDisconnectFn(ctx, networkID, containerID, force)
	}
	return nil
}

// NetworkRemove fakes removing a network.
func (a *API) NetworkRemove(ctx context.Context, networkID string) error {
	if a.NetworkRemoveFn != nil {
		return a.NetworkRemoveFn(ctx, networkID)
	}
	return nil
}

// VolumeCreate fakes creating a volume.
func (a *API) VolumeCreate(ctx context.Context, options volume.CreateOptions) (volume.Volume, error) {
	if a.VolumeCreateFn != nil {
		return a.VolumeCreateFn(ctx, options)
	}
	return volume.Volume{Name: options.Name}, nil
}

// VolumeRemove fakes removing a volume.
func (a *API) VolumeRemove(ctx context.Context, volumeID string, force bool) error {
	if a.VolumeRemoveFn != nil {
		return a.VolumeRemoveFn(ctx, volumeID, force)
	}
	return nil
}

// Close fakes closing the underlying Docker client connection.
func (a *API) Close() error {
	if a.CloseFn != nil {
		return a.CloseFn()
	}
	return nil
}
