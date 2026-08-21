// Package dockerfake provides a fake implementation of dockerctl.API for
// use in tests, so dockerctl.Ctl (and its callers, e.g. cmd/osa and
// internal/orgs) can be exercised without a real Docker daemon.
package dockerfake

import (
	"context"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/ownscalenet/ownscale/internal/dockerctl"
)

// API is a fake dockerctl.API. It embeds the Docker SDK's own client
// interfaces as nil values, so it only needs to implement the handful of
// methods Ctl actually calls; every field below defaults to a reasonable
// success response when left nil, but can be set to a func to observe
// calls or return specific results/errors. Any method not listed below
// (i.e. not used by Ctl) will panic with a nil pointer dereference if ever
// called, which is a clear signal that a test is exercising a code path
// this fake doesn't support yet.
type API struct {
	client.ContainerAPIClient
	client.NetworkAPIClient
	client.VolumeAPIClient

	ContainerCreateFn  func(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStartFn   func(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerStopFn    func(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemoveFn  func(ctx context.Context, containerID string, options container.RemoveOptions) error
	ContainerInspectFn func(ctx context.Context, containerID string) (container.InspectResponse, error)
	ContainerListFn    func(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	CopyToContainerFn  func(ctx context.Context, containerID, path string, content io.Reader, options container.CopyToContainerOptions) error

	NetworkInspectFn func(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error)
	NetworkCreateFn  func(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error)

	VolumeCreateFn func(ctx context.Context, options volume.CreateOptions) (volume.Volume, error)
	VolumeRemoveFn func(ctx context.Context, volumeID string, force bool) error

	CloseFn func() error
}

var _ dockerctl.API = (*API)(nil)

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
