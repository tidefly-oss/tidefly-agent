package agent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
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
	t := 10
	return r.docker.ContainerRestart(ctx, containerID, container.StopOptions{Timeout: &t})
}

func (r *RuntimeClient) RemoveContainer(ctx context.Context, containerID string, force bool) error {
	return r.docker.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: force})
}

func (r *RuntimeClient) StreamLogs(ctx context.Context, containerID string, follow bool, tailLines int32) (<-chan LogLine, error) {
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
		defer reader.Close()
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

func (r *RuntimeClient) Deploy(ctx context.Context, cmd *agentpb.CmdDeploy) (string, error) {
	out, err := r.docker.ImagePull(ctx, cmd.Image, image.PullOptions{})
	if err != nil {
		return "", fmt.Errorf("pull image %s: %w", cmd.Image, err)
	}
	defer out.Close()
	io.Copy(io.Discard, out)

	portBindings := nat.PortMap{}
	exposedPorts := nat.PortSet{}
	for _, p := range cmd.Ports {
		cp := nat.Port(fmt.Sprintf("%d/%s", p.ContainerPort, p.Protocol))
		exposedPorts[cp] = struct{}{}
		if p.HostPort > 0 {
			portBindings[cp] = []nat.PortBinding{{HostPort: fmt.Sprintf("%d", p.HostPort)}}
		}
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

	resp, err := r.docker.ContainerCreate(ctx,
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
		return "", fmt.Errorf("create container: %w", err)
	}
	if err := r.docker.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}
	return resp.ID[:12], nil
}

var _ = filters.Args{}
