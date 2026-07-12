package agent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	dockernetwork "github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	agentpb "github.com/tidefly-oss/tidefly-agent/internal/proto"
)

type LogLine struct {
	Text     string
	IsStderr bool
}

type RuntimeClient struct {
	docker *dockerclient.Client
}

func NewRuntimeClient(runtimeType, socketPath string) *RuntimeClient {
	opts := []dockerclient.Opt{
		dockerclient.WithAPIVersionNegotiation(),
	}
	if socketPath != "" {
		opts = append(opts, dockerclient.WithHost("unix://"+socketPath))
	}
	c, _ := dockerclient.NewClientWithOpts(opts...)
	return &RuntimeClient{docker: c}
}

// ensureNetwork creates a bridge network on the worker if it doesn't
// already exist. Worker hosts don't share the plane's local Docker
// networks (they're separate machines), so a deploy referencing a network
// name that hasn't been created there yet must provision it on demand
// rather than fail.
func (r *RuntimeClient) ensureNetwork(ctx context.Context, name string) error {
	if _, err := r.docker.NetworkCreate(ctx, name, dockernetwork.CreateOptions{Driver: "bridge"}); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return err
	}
	return nil
}

func (r *RuntimeClient) ListContainers(ctx context.Context) ([]*agentpb.Container, error) {
	list, err := r.docker.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	result := make([]*agentpb.Container, 0, len(list))
	for _, c := range list {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		ports := make([]*agentpb.PortBinding, 0, len(c.Ports))
		for _, p := range c.Ports {
			ports = append(ports, &agentpb.PortBinding{
				HostIp:        p.IP,
				HostPort:      int32(p.PublicPort),
				ContainerPort: int32(p.PrivatePort),
				Protocol:      p.Type,
			})
		}
		result = append(result, &agentpb.Container{
			Id:      c.ID[:12],
			Name:    name,
			Image:   c.Image,
			Status:  c.Status,
			State:   c.State,
			Created: c.Created * 1000,
			Labels:  c.Labels,
			Ports:   ports,
		})
	}
	return result, nil
}

// ListServiceContainers returns all containers belonging to a service
// identified by the tidefly.service_name label.
func (r *RuntimeClient) ListServiceContainers(ctx context.Context, serviceName string) ([]*agentpb.Container, error) {
	all, err := r.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	var result []*agentpb.Container
	for _, ct := range all {
		if ct.Labels["tidefly.service_name"] == serviceName {
			result = append(result, ct)
		}
	}
	return result, nil
}

// RemoveServiceContainers stops and removes all containers for a service.
func (r *RuntimeClient) RemoveServiceContainers(ctx context.Context, serviceName string) error {
	f := filters.NewArgs(filters.Arg("label", "tidefly.service_name="+serviceName))
	list, err := r.docker.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return err
	}
	for _, ct := range list {
		_ = r.docker.ContainerStop(ctx, ct.ID, container.StopOptions{Timeout: new(10)})
		_ = r.docker.ContainerRemove(ctx, ct.ID, container.RemoveOptions{Force: true})
	}
	return nil
}

// RemoveNamedContainer stops and removes a single container by name.
func (r *RuntimeClient) RemoveNamedContainer(ctx context.Context, name string) error {
	_ = r.docker.ContainerStop(ctx, name, container.StopOptions{Timeout: new(10)})
	return r.docker.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
}

func (r *RuntimeClient) StartContainer(ctx context.Context, containerID string) error {
	return r.docker.ContainerStart(ctx, containerID, container.StartOptions{})
}

func (r *RuntimeClient) StopContainer(ctx context.Context, containerID string, timeoutSec int32) error {
	t := int(timeoutSec)
	if t == 0 {
		t = 10
	}
	return r.docker.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &t})
}

func (r *RuntimeClient) RestartContainer(ctx context.Context, containerID string) error {
	return r.docker.ContainerRestart(ctx, containerID, container.StopOptions{Timeout: new(10)})
}

func (r *RuntimeClient) RemoveContainer(ctx context.Context, containerID string, force bool) error {
	return r.docker.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: force})
}

func (r *RuntimeClient) StreamLogs(
	ctx context.Context,
	containerID string,
	follow bool,
	tailLines int32,
) (<-chan LogLine, error) {
	tail := "50"
	if tailLines > 0 {
		tail = fmt.Sprintf("%d", tailLines)
	}
	reader, err := r.docker.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tail,
	})
	if err != nil {
		return nil, err
	}
	ch := make(chan LogLine, 100)
	go func() {
		defer close(ch)
		defer func() { _ = reader.Close() }()
		buf := make([]byte, 8192)
		for {
			header := make([]byte, 8)
			if _, err := io.ReadFull(reader, header); err != nil {
				return
			}
			isStderr := header[0] == 2
			size := int(header[4])<<24 | int(header[5])<<16 | int(header[6])<<8 | int(header[7])
			if size > len(buf) {
				buf = make([]byte, size)
			}
			if _, err := io.ReadFull(reader, buf[:size]); err != nil {
				return
			}
			line := strings.TrimRight(string(buf[:size]), "\n")
			select {
			case ch <- LogLine{Text: line, IsStderr: isStderr}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (r *RuntimeClient) Deploy(ctx context.Context, cmd *agentpb.CmdDeploy) (string, int32, error) {
	out, err := r.docker.ImagePull(ctx, cmd.Image, image.PullOptions{})
	if err != nil {
		return "", 0, fmt.Errorf("pull image %s: %w", cmd.Image, err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(io.Discard, out); err != nil {
		return "", 0, fmt.Errorf("drain pull output: %w", err)
	}

	portBindings := nat.PortMap{}
	exposedPorts := nat.PortSet{}
	var primaryContainerPort nat.Port
	for _, p := range cmd.Ports {
		cp := nat.Port(fmt.Sprintf("%d/%s", p.ContainerPort, p.Protocol))
		exposedPorts[cp] = struct{}{}
		if primaryContainerPort == "" {
			primaryContainerPort = cp
		}
		// Always publish, even with HostPort==0 — Docker auto-assigns a free
		// host port in that case. This is what lets a worker-hosted service
		// be reachable at all: unlike local (same-host) services, Traefik
		// can't resolve the container by Docker network name across hosts,
		// it needs worker_ip:host_port.
		hostPort := ""
		if p.HostPort > 0 {
			hostPort = fmt.Sprintf("%d", p.HostPort)
		}
		portBindings[cp] = []nat.PortBinding{{HostPort: hostPort}}
	}

	resources := container.Resources{}
	if cmd.Limits != nil {
		resources.Memory = cmd.Limits.MemoryBytes
		resources.CPUShares = int64(cmd.Limits.CpuShares)
	}

	labels := map[string]string{
		"tidefly.project_id":   cmd.ProjectId,
		"tidefly.service_name": cmd.ServiceName,
		"tidefly.managed":      "true",
	}
	for k, v := range cmd.Labels {
		labels[k] = v
	}

	containerName := fmt.Sprintf("tidefly_%s_%s", cmd.ProjectId, cmd.ServiceName)
	_ = r.docker.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})

	if cmd.Network != "" {
		if err := r.ensureNetwork(ctx, cmd.Network); err != nil {
			return "", 0, fmt.Errorf("ensure network %q: %w", cmd.Network, err)
		}
	}

	resp, err := r.docker.ContainerCreate(
		ctx,
		&container.Config{
			Image:        cmd.Image,
			Env:          cmd.Env,
			Labels:       labels,
			ExposedPorts: exposedPorts,
		},
		&container.HostConfig{
			PortBindings: portBindings,
			NetworkMode:  container.NetworkMode(cmd.Network),
			Resources:    resources,
		},
		nil, nil, containerName,
	)
	if err != nil {
		return "", 0, fmt.Errorf("create container: %w", err)
	}
	if err := r.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", 0, fmt.Errorf("start container: %w", err)
	}

	var assignedPort int32
	if primaryContainerPort != "" {
		assignedPort = r.readAssignedPort(ctx, resp.ID, primaryContainerPort)
	}

	return resp.ID[:12], assignedPort, nil
}

// readAssignedPort inspects a just-started container to find the host port
// Docker actually bound the given container port to — needed because a
// dynamic request (HostPort==0 at create time) doesn't tell us the result
// until the container is running.
func (r *RuntimeClient) readAssignedPort(ctx context.Context, containerID string, containerPort nat.Port) int32 {
	info, err := r.docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return 0
	}
	bindings, ok := info.NetworkSettings.Ports[containerPort]
	if !ok || len(bindings) == 0 {
		return 0
	}
	var port int
	_, _ = fmt.Sscanf(bindings[0].HostPort, "%d", &port)
	return int32(port)
}
