package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/tidefly-oss/tidefly-agent/internal/config"
	agentpb "github.com/tidefly-oss/tidefly-agent/internal/proto"
)

type CommandHandler struct {
	cfg    *config.Config
	stream agentpb.AgentService_ConnectClient
	rt     *RuntimeClient
	caddy  *CaddyClient
}

func NewCommandHandler(cfg *config.Config, stream agentpb.AgentService_ConnectClient) *CommandHandler {
	return &CommandHandler{
		cfg:    cfg,
		stream: stream,
		rt:     NewRuntimeClient(cfg.Runtime.Type, cfg.Runtime.SocketPath),
		caddy:  NewCaddyClient(cfg.Caddy.AdminURL),
	}
}

func (h *CommandHandler) Handle(ctx context.Context, msg *agentpb.PlaneMessage) error {
	slog.Debug("agent: received command", "command_id", msg.CommandId, "type", fmt.Sprintf("%T", msg.Payload))
	switch p := msg.Payload.(type) {
	case *agentpb.PlaneMessage_Ack:
		if !p.Ack.Accepted {
			return fmt.Errorf("plane rejected connection: %s", p.Ack.Reason)
		}
		slog.Info("agent: connection accepted by plane")
		return nil

	case *agentpb.PlaneMessage_ListContainers:
		return h.handleListContainers(ctx, msg.CommandId)

	case *agentpb.PlaneMessage_StartContainer:
		return h.handleStartContainer(ctx, msg.CommandId, p.StartContainer.ContainerId)

	case *agentpb.PlaneMessage_StopContainer:
		return h.handleStopContainer(ctx, msg.CommandId, p.StopContainer.ContainerId, p.StopContainer.TimeoutSec)

	case *agentpb.PlaneMessage_RestartContainer:
		return h.handleRestartContainer(ctx, msg.CommandId, p.RestartContainer.ContainerId)

	case *agentpb.PlaneMessage_RemoveContainer:
		return h.handleRemoveContainer(ctx, msg.CommandId, p.RemoveContainer.ContainerId, p.RemoveContainer.Force)

	case *agentpb.PlaneMessage_StreamLogs:
		go h.handleStreamLogs(ctx, msg.CommandId, p.StreamLogs)
		return nil

	case *agentpb.PlaneMessage_Deploy:
		go h.handleDeploy(ctx, msg.CommandId, p.Deploy)
		return nil

	case *agentpb.PlaneMessage_CollectMetrics:
		return h.handleCollectMetrics(ctx, msg.CommandId)

	case *agentpb.PlaneMessage_RegisterRoute:
		return h.handleRegisterRoute(ctx, msg.CommandId, p.RegisterRoute)

	case *agentpb.PlaneMessage_RemoveRoute:
		return h.handleRemoveRoute(ctx, msg.CommandId, p.RemoveRoute)

	case *agentpb.PlaneMessage_Heal:
		go h.handleHeal(ctx, msg.CommandId, p.Heal)
		return nil

	case *agentpb.PlaneMessage_BlueGreen:
		go h.handleBlueGreen(ctx, msg.CommandId, p.BlueGreen)
		return nil

	case *agentpb.PlaneMessage_RollingUpdate:
		go h.handleRollingUpdate(ctx, msg.CommandId, p.RollingUpdate)
		return nil

	case *agentpb.PlaneMessage_Autoscale:
		go h.handleAutoscale(ctx, msg.CommandId, p.Autoscale)
		return nil

	default:
		slog.Warn("agent: unknown command type", "type", fmt.Sprintf("%T", msg.Payload))
		return nil
	}
}

// ── Container commands ────────────────────────────────────────────────────────

func (h *CommandHandler) handleListContainers(ctx context.Context, cmdID string) error {
	containers, err := h.rt.ListContainers(ctx)
	if err != nil {
		return h.sendError(cmdID, "LIST_CONTAINERS_FAILED", err.Error())
	}
	return h.stream.Send(
		&agentpb.AgentMessage{
			MessageId: uuid.New().String(),
			Payload: &agentpb.AgentMessage_ContainerList{
				ContainerList: &agentpb.ContainerListResult{
					CommandId:  cmdID,
					Containers: containers,
				},
			},
		},
	)
}

func (h *CommandHandler) handleStartContainer(ctx context.Context, cmdID, containerID string) error {
	if err := h.rt.StartContainer(ctx, containerID); err != nil {
		return h.sendError(cmdID, "START_FAILED", err.Error())
	}
	return h.sendAck(cmdID, true, "")
}

func (h *CommandHandler) handleStopContainer(ctx context.Context, cmdID, containerID string, timeoutSec int32) error {
	if err := h.rt.StopContainer(ctx, containerID, timeoutSec); err != nil {
		return h.sendError(cmdID, "STOP_FAILED", err.Error())
	}
	return h.sendAck(cmdID, true, "")
}

func (h *CommandHandler) handleRestartContainer(ctx context.Context, cmdID, containerID string) error {
	if err := h.rt.RestartContainer(ctx, containerID); err != nil {
		return h.sendError(cmdID, "RESTART_FAILED", err.Error())
	}
	return h.sendAck(cmdID, true, "")
}

func (h *CommandHandler) handleRemoveContainer(ctx context.Context, cmdID, containerID string, force bool) error {
	if err := h.rt.RemoveContainer(ctx, containerID, force); err != nil {
		return h.sendError(cmdID, "REMOVE_FAILED", err.Error())
	}
	return h.sendAck(cmdID, true, "")
}

// ── Logs ──────────────────────────────────────────────────────────────────────

func (h *CommandHandler) handleStreamLogs(ctx context.Context, cmdID string, cmd *agentpb.CmdStreamLogs) {
	logCh, err := h.rt.StreamLogs(ctx, cmd.ContainerId, cmd.Follow, cmd.TailLines)
	if err != nil {
		_ = h.sendError(cmdID, "LOGS_FAILED", err.Error())
		return
	}
	for line := range logCh {
		_ = h.stream.Send(
			&agentpb.AgentMessage{
				MessageId: uuid.New().String(),
				Payload: &agentpb.AgentMessage_ContainerLogs{
					ContainerLogs: &agentpb.ContainerLogsResult{
						CommandId:   cmdID,
						ContainerId: cmd.ContainerId,
						Line:        line.Text,
						IsStderr:    line.IsStderr,
						Timestamp:   time.Now().UnixMilli(),
					},
				},
			},
		)
	}
}

// ── Deploy ────────────────────────────────────────────────────────────────────

func (h *CommandHandler) handleDeploy(ctx context.Context, cmdID string, cmd *agentpb.CmdDeploy) {
	containerID, err := h.rt.Deploy(ctx, cmd)
	if err != nil {
		_ = h.stream.Send(&agentpb.AgentMessage{
			MessageId: uuid.New().String(),
			Payload: &agentpb.AgentMessage_DeployResult{
				DeployResult: &agentpb.DeployResult{
					CommandId: cmdID,
					Success:   false,
					Error:     err.Error(),
				},
			},
		})
		return
	}
	_ = h.stream.Send(&agentpb.AgentMessage{
		MessageId: uuid.New().String(),
		Payload: &agentpb.AgentMessage_DeployResult{
			DeployResult: &agentpb.DeployResult{
				CommandId:   cmdID,
				Success:     true,
				ContainerId: containerID,
			},
		},
	})
}

// ── Self-Heal ─────────────────────────────────────────────────────────────────

func (h *CommandHandler) handleHeal(ctx context.Context, cmdID string, cmd *agentpb.CmdHeal) {
	slog.Info("agent: self-heal triggered", "service", cmd.ServiceName, "reason", cmd.Reason)

	// Remove existing containers for this service
	if err := h.rt.RemoveServiceContainers(ctx, cmd.ServiceName); err != nil {
		slog.Warn("agent: heal — remove containers failed", "service", cmd.ServiceName, "error", err)
	}

	// Wait briefly before restart
	time.Sleep(2 * time.Second)

	containerID, err := h.rt.Deploy(ctx, cmd.Deploy)
	if err != nil {
		slog.Error("agent: self-heal failed", "service", cmd.ServiceName, "error", err)
		_ = h.stream.Send(&agentpb.AgentMessage{
			MessageId: uuid.New().String(),
			Payload: &agentpb.AgentMessage_HealResult{
				HealResult: &agentpb.HealResult{
					CommandId: cmdID,
					Success:   false,
					Error:     err.Error(),
				},
			},
		})
		return
	}

	slog.Info("agent: self-heal complete", "service", cmd.ServiceName, "container", containerID)
	_ = h.stream.Send(&agentpb.AgentMessage{
		MessageId: uuid.New().String(),
		Payload: &agentpb.AgentMessage_HealResult{
			HealResult: &agentpb.HealResult{
				CommandId:   cmdID,
				Success:     true,
				ContainerId: containerID,
			},
		},
	})
}

// ── Blue-Green ────────────────────────────────────────────────────────────────

func (h *CommandHandler) handleBlueGreen(ctx context.Context, cmdID string, cmd *agentpb.CmdBlueGreen) {
	slog.Info("agent: blue-green deploy", "service", cmd.ServiceName, "current_slot", cmd.CurrentSlot)

	newSlot := "green"
	if cmd.CurrentSlot == "green" {
		newSlot = "blue"
	}
	newName := fmt.Sprintf("%s-%s", cmd.ServiceName, newSlot)

	// Override service name in deploy spec to use slot name
	deployCmd := cmd.Deploy
	deployCmd.ServiceName = newName
	deployCmd.Labels["tidefly.slot"] = newSlot

	containerID, err := h.rt.Deploy(ctx, deployCmd)
	if err != nil {
		slog.Error("agent: blue-green deploy new slot failed", "service", cmd.ServiceName, "error", err)
		_ = h.sendHealResult(cmdID, false, "", err.Error())
		return
	}

	// Wait for container to be running
	if err := h.waitRunning(ctx, newName, 60*time.Second); err != nil {
		_ = h.rt.RemoveContainer(ctx, containerID, true)
		_ = h.sendHealResult(cmdID, false, "", fmt.Sprintf("new container unhealthy: %s", err))
		return
	}

	// Switch Caddy route if domain configured
	if cmd.Domain != "" && h.cfg.Caddy.Enabled {
		upstream := fmt.Sprintf("%s:%d", newName, cmd.Port)
		if err := h.caddy.RegisterRoute(ctx, upstream, cmd.Domain, cmd.Tls); err != nil {
			slog.Warn("agent: blue-green route switch failed", "error", err)
			// Don't rollback — route failure is non-fatal, container is running
		} else {
			slog.Info("agent: blue-green route switched", "domain", cmd.Domain, "upstream", upstream)
		}
	}

	// Remove old containers
	oldName := cmd.ServiceName
	if cmd.CurrentSlot != "" {
		oldName = fmt.Sprintf("%s-%s", cmd.ServiceName, cmd.CurrentSlot)
	}
	if err := h.rt.RemoveNamedContainer(ctx, oldName); err != nil {
		slog.Warn("agent: blue-green remove old container failed", "name", oldName, "error", err)
	}

	slog.Info("agent: blue-green complete", "service", cmd.ServiceName, "active_slot", newSlot)
	_ = h.sendHealResult(cmdID, true, containerID, "")
}

// ── Rolling Update ────────────────────────────────────────────────────────────

func (h *CommandHandler) handleRollingUpdate(ctx context.Context, cmdID string, cmd *agentpb.CmdRollingUpdate) {
	slog.Info("agent: rolling update", "service", cmd.ServiceName, "replicas", cmd.Replicas)

	containers, err := h.rt.ListServiceContainers(ctx, cmd.ServiceName)
	if err != nil {
		_ = h.sendScaleResult(cmdID, false, 0, err.Error())
		return
	}

	updated := 0
	for _, ct := range containers {
		// Stop old
		_ = h.rt.StopContainer(ctx, ct.Id, 10)
		_ = h.rt.RemoveContainer(ctx, ct.Id, true)

		// Deploy new
		deployCmd := cmd.Deploy
		if len(containers) > 1 {
			deployCmd.ServiceName = fmt.Sprintf("%s-%d", cmd.ServiceName, updated+1)
		}
		if _, err := h.rt.Deploy(ctx, deployCmd); err != nil {
			slog.Error("agent: rolling update failed", "service", cmd.ServiceName, "error", err)
			_ = h.sendScaleResult(cmdID, false, int32(updated), err.Error())
			return
		}
		updated++
		time.Sleep(2 * time.Second)
	}

	// Scale up if needed
	for updated < int(cmd.Replicas) {
		deployCmd := cmd.Deploy
		deployCmd.ServiceName = fmt.Sprintf("%s-%d", cmd.ServiceName, updated+1)
		if _, err := h.rt.Deploy(ctx, deployCmd); err != nil {
			_ = h.sendScaleResult(cmdID, false, int32(updated), err.Error())
			return
		}
		updated++
	}

	slog.Info("agent: rolling update complete", "service", cmd.ServiceName, "replicas", updated)
	_ = h.sendScaleResult(cmdID, true, int32(updated), "")
}

// ── Autoscale ─────────────────────────────────────────────────────────────────

func (h *CommandHandler) handleAutoscale(ctx context.Context, cmdID string, cmd *agentpb.CmdAutoscale) {
	slog.Info("agent: autoscale", "service", cmd.ServiceName,
		"current", cmd.CurrentReplicas, "target", cmd.TargetReplicas)

	current := int(cmd.CurrentReplicas)
	target := int(cmd.TargetReplicas)

	if target > current {
		// Scale up
		for i := current + 1; i <= target; i++ {
			deployCmd := cmd.Deploy
			deployCmd.ServiceName = fmt.Sprintf("%s-%d", cmd.ServiceName, i)
			deployCmd.Labels["tidefly.replica"] = fmt.Sprintf("%d", i)
			if _, err := h.rt.Deploy(ctx, deployCmd); err != nil {
				slog.Error("agent: autoscale up failed", "service", cmd.ServiceName, "replica", i, "error", err)
				_ = h.sendScaleResult(cmdID, false, int32(i-1), err.Error())
				return
			}
			slog.Info("agent: scaled up", "service", cmd.ServiceName, "replica", i)
		}
	} else if target < current {
		// Scale down — remove highest numbered replicas first
		containers, err := h.rt.ListServiceContainers(ctx, cmd.ServiceName)
		if err != nil {
			_ = h.sendScaleResult(cmdID, false, cmd.CurrentReplicas, err.Error())
			return
		}
		toRemove := current - target
		removed := 0
		for i := len(containers) - 1; i >= 0 && removed < toRemove; i-- {
			ct := containers[i]
			if ct.Name == cmd.ServiceName {
				continue // never remove the primary container
			}
			_ = h.rt.StopContainer(ctx, ct.Id, 10)
			_ = h.rt.RemoveContainer(ctx, ct.Id, true)
			removed++
			slog.Info("agent: scaled down", "service", cmd.ServiceName, "removed", ct.Name)
		}
	}

	_ = h.sendScaleResult(cmdID, true, cmd.TargetReplicas, "")
}

// ── Metrics ───────────────────────────────────────────────────────────────────

func (h *CommandHandler) handleCollectMetrics(_ context.Context, cmdID string) error {
	m, err := CollectMetrics()
	if err != nil {
		return h.sendError(cmdID, "METRICS_FAILED", err.Error())
	}
	return h.stream.Send(
		&agentpb.AgentMessage{
			MessageId: uuid.New().String(),
			Payload: &agentpb.AgentMessage_Metrics{
				Metrics: &agentpb.MetricsResult{
					CommandId:   cmdID,
					CpuPercent:  m.CPUPercent,
					MemUsedMb:   m.MemUsedMB,
					MemTotalMb:  m.MemTotalMB,
					DiskUsedGb:  m.DiskUsedGB,
					DiskTotalGb: m.DiskTotalGB,
					NetRxMb:     m.NetRxMB,
					NetTxMb:     m.NetTxMB,
				},
			},
		},
	)
}

// ── Routes ────────────────────────────────────────────────────────────────────

func (h *CommandHandler) handleRegisterRoute(ctx context.Context, cmdID string, cmd *agentpb.CmdRegisterRoute) error {
	if !h.cfg.Caddy.Enabled {
		return h.sendAck(cmdID, false, "caddy is disabled on this worker")
	}
	if err := h.caddy.RegisterRoute(ctx, cmd.Upstream, cmd.Domain, cmd.Tls); err != nil {
		return h.sendError(cmdID, "ROUTE_REGISTER_FAILED", err.Error())
	}
	return h.sendAck(cmdID, true, "")
}

func (h *CommandHandler) handleRemoveRoute(ctx context.Context, cmdID string, cmd *agentpb.CmdRemoveRoute) error {
	if !h.cfg.Caddy.Enabled {
		return h.sendAck(cmdID, false, "caddy is disabled on this worker")
	}
	if err := h.caddy.RemoveRoute(ctx, cmd.Domain); err != nil {
		return h.sendError(cmdID, "ROUTE_REMOVE_FAILED", err.Error())
	}
	return h.sendAck(cmdID, true, "")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (h *CommandHandler) waitRunning(ctx context.Context, containerName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		containers, err := h.rt.ListContainers(ctx)
		if err != nil {
			return err
		}
		for _, ct := range containers {
			if ct.Name == containerName && ct.State == "running" {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("container %q did not become running within %s", containerName, timeout)
}

func (h *CommandHandler) sendHealResult(cmdID string, success bool, containerID, errMsg string) error {
	return h.stream.Send(&agentpb.AgentMessage{
		MessageId: uuid.New().String(),
		Payload: &agentpb.AgentMessage_HealResult{
			HealResult: &agentpb.HealResult{
				CommandId:   cmdID,
				Success:     success,
				ContainerId: containerID,
				Error:       errMsg,
			},
		},
	})
}

func (h *CommandHandler) sendScaleResult(cmdID string, success bool, replicas int32, errMsg string) error {
	return h.stream.Send(&agentpb.AgentMessage{
		MessageId: uuid.New().String(),
		Payload: &agentpb.AgentMessage_ScaleResult{
			ScaleResult: &agentpb.ScaleResult{
				CommandId: cmdID,
				Success:   success,
				Replicas:  replicas,
				Error:     errMsg,
			},
		},
	})
}

func (h *CommandHandler) sendAck(cmdID string, accepted bool, reason string) error {
	return h.stream.Send(
		&agentpb.AgentMessage{
			MessageId: uuid.New().String(),
			Payload: &agentpb.AgentMessage_CommandAck{
				CommandAck: &agentpb.CommandAck{
					CommandId: cmdID,
					Accepted:  accepted,
					Reason:    reason,
				},
			},
		},
	)
}

func (h *CommandHandler) sendError(cmdID, code, message string) error {
	return h.stream.Send(
		&agentpb.AgentMessage{
			MessageId: uuid.New().String(),
			Payload: &agentpb.AgentMessage_Error{
				Error: &agentpb.ErrorEvent{
					CommandId: cmdID,
					Code:      code,
					Message:   message,
				},
			},
		},
	)
}
