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

// CommandHandler processes PlaneMessages and sends results back via the stream.
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
		// Plane acknowledged our hello
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
	return h.stream.Send(&agentpb.AgentMessage{
		MessageId: uuid.New().String(),
		Payload: &agentpb.AgentMessage_ContainerList{
			ContainerList: &agentpb.ContainerListResult{
				CommandId:  cmdID,
				Containers: containers,
			},
		},
	})
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
		_ = h.stream.Send(&agentpb.AgentMessage{
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
		})
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

// ── Metrics ───────────────────────────────────────────────────────────────────

func (h *CommandHandler) handleCollectMetrics(ctx context.Context, cmdID string) error {
	m, err := CollectMetrics()
	if err != nil {
		return h.sendError(cmdID, "METRICS_FAILED", err.Error())
	}
	return h.stream.Send(&agentpb.AgentMessage{
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
	})
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

// ── helpers ───────────────────────────────────────────────────────────────────

func (h *CommandHandler) sendAck(cmdID string, accepted bool, reason string) error {
	return h.stream.Send(&agentpb.AgentMessage{
		MessageId: uuid.New().String(),
		Payload: &agentpb.AgentMessage_CommandAck{
			CommandAck: &agentpb.CommandAck{
				CommandId: cmdID,
				Accepted:  accepted,
				Reason:    reason,
			},
		},
	})
}

func (h *CommandHandler) sendError(cmdID, code, message string) error {
	return h.stream.Send(&agentpb.AgentMessage{
		MessageId: uuid.New().String(),
		Payload: &agentpb.AgentMessage_Error{
			Error: &agentpb.ErrorEvent{
				CommandId: cmdID,
				Code:      code,
				Message:   message,
			},
		},
	})
}
