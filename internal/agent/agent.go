package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/google/uuid"
	"github.com/tidefly-oss/tidefly-agent/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	// proto is copied from tidefly-backend (or use a shared module later)
	agentpb "github.com/tidefly-oss/tidefly-agent/internal/proto"
)

// Agent manages the lifecycle: registration → connect → command loop.
type Agent struct {
	cfg     *config.Config
	version string
	id      string
}

func New(cfg *config.Config, version string) (*Agent, error) {
	id := cfg.Agent.ID
	if id == "" {
		id = uuid.New().String()
	}
	return &Agent{cfg: cfg, version: version, id: id}, nil
}

// Run is the main loop:
// 1. Register with Plane if not already registered (get mTLS cert)
// 2. Connect via gRPC mTLS
// 3. Handle commands, send heartbeats
// 4. On disconnect: retry with backoff
func (a *Agent) Run(ctx context.Context) error {
	if !a.cfg.IsRegistered() {
		slog.Info("agent: not registered, starting registration")
		if err := a.register(ctx); err != nil {
			return fmt.Errorf("registration failed: %w", err)
		}
		slog.Info("agent: registration complete")
	}

	// Start cert renewal loop (checks daily, renews if < 30 days left)
	go a.startRenewalLoop(ctx)

	// Connect loop with retry
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		slog.Info("agent: connecting to plane", "endpoint", a.cfg.Plane.Endpoint)
		if err := a.connect(ctx); err != nil {
			slog.Error("agent: connection lost", "error", err)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(a.cfg.Plane.RetryDelay):
			slog.Info("agent: retrying connection", "delay", a.cfg.Plane.RetryDelay)
		}
	}
}

// ── Registration ──────────────────────────────────────────────────────────────

type registerRequest struct {
	Token        string `json:"token"`
	WorkerID     string `json:"worker_id"`
	Name         string `json:"name"`
	AgentVersion string `json:"agent_version"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	RuntimeType  string `json:"runtime_type"`
}

type registerResponse struct {
	WorkerID  string `json:"worker_id"`
	CertPEM   string `json:"cert_pem"`
	KeyPEM    string `json:"key_pem"`
	CACertPEM string `json:"ca_cert_pem"`
	ExpiresAt string `json:"expires_at"`
}

func (a *Agent) register(ctx context.Context) error {
	if a.cfg.Plane.Token == "" {
		return fmt.Errorf("PLANE_TOKEN is required for registration")
	}

	body, _ := json.Marshal(registerRequest{
		Token:        a.cfg.Plane.Token,
		WorkerID:     a.id,
		Name:         a.cfg.Agent.Name,
		AgentVersion: a.version,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		RuntimeType:  a.cfg.Runtime.Type,
	})

	// Registration goes over HTTPS (plain TLS, not mTLS — no client cert yet)
	// Plane endpoint for HTTP is derived from gRPC endpoint
	httpEndpoint := grpcToHTTP(a.cfg.Plane.Endpoint)
	url := httpEndpoint + "/api/v1/agent/register"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("registration request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("registration failed (%d): %s", resp.StatusCode, string(b))
	}

	var reg registerResponse
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return fmt.Errorf("decode registration response: %w", err)
	}

	// Persist certs to disk
	if err := os.MkdirAll(a.cfg.CertDir(), 0700); err != nil {
		return fmt.Errorf("create cert dir: %w", err)
	}
	if err := os.WriteFile(a.cfg.Plane.CertFile, []byte(reg.CertPEM), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(a.cfg.Plane.KeyFile, []byte(reg.KeyPEM), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(a.cfg.Plane.CAFile, []byte(reg.CACertPEM), 0600); err != nil {
		return err
	}

	// Persist worker ID
	a.id = reg.WorkerID
	slog.Info("agent: certs saved", "cert", a.cfg.Plane.CertFile, "expires_at", reg.ExpiresAt)

	// Remove token from .env — it's one-time use only
	clearRegistrationToken(".env")

	return nil
}

// ── gRPC Connect ──────────────────────────────────────────────────────────────

func (a *Agent) connect(ctx context.Context) error {
	tlsCfg, err := a.buildTLSConfig()
	if err != nil {
		return fmt.Errorf("build TLS config: %w", err)
	}

	conn, err := grpc.NewClient(
		a.cfg.Plane.Endpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
	)
	if err != nil {
		return fmt.Errorf("grpc dial: %w", err)
	}
	defer conn.Close()

	client := agentpb.NewAgentServiceClient(conn)

	stream, err := client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	slog.Info("agent: stream opened, sending hello")

	// Send AgentHello
	if err := stream.Send(&agentpb.AgentMessage{
		MessageId: uuid.New().String(),
		WorkerId:  a.id,
		Payload: &agentpb.AgentMessage_Hello{
			Hello: &agentpb.AgentHello{
				AgentVersion: a.version,
				Os:           runtime.GOOS,
				Arch:         runtime.GOARCH,
				RuntimeType:  a.cfg.Runtime.Type,
				Hostname:     a.cfg.Agent.Name,
			},
		},
	}); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	// Start heartbeat goroutine
	heartbeatDone := make(chan struct{})
	go a.heartbeatLoop(ctx, stream, heartbeatDone)
	defer close(heartbeatDone)

	// Receive loop
	handler := NewCommandHandler(a.cfg, stream)
	for {
		msg, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("recv: %w", err)
		}
		if err := handler.Handle(ctx, msg); err != nil {
			slog.Error("agent: handle command error", "error", err)
		}
	}
}

func (a *Agent) heartbeatLoop(ctx context.Context, stream agentpb.AgentService_ConnectClient, done chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			_ = stream.Send(&agentpb.AgentMessage{
				MessageId: uuid.New().String(),
				WorkerId:  a.id,
				Payload: &agentpb.AgentMessage_Heartbeat{
					Heartbeat: &agentpb.AgentHeartbeat{
						Timestamp: time.Now().UnixMilli(),
					},
				},
			})
		}
	}
}

// ── TLS ───────────────────────────────────────────────────────────────────────

func (a *Agent) buildTLSConfig() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(a.cfg.Plane.CertFile, a.cfg.Plane.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load client cert: %w", err)
	}

	caPEM, err := os.ReadFile(a.cfg.Plane.CAFile)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}

	block, _ := pem.Decode(caPEM)
	if block == nil {
		return nil, fmt.Errorf("decode CA PEM")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// grpcToHTTP converts "plane.example.com:7443" → "https://plane.example.com:8181"
// The HTTP port is derived from the standard Tidefly backend port.
// Users can override via PLANE_HTTP_ENDPOINT env var.
func grpcToHTTP(grpcEndpoint string) string {
	if v := os.Getenv("PLANE_HTTP_ENDPOINT"); v != "" {
		return v
	}
	// Strip gRPC port, use HTTPS on standard port
	// e.g. plane.example.com:7443 → https://plane.example.com
	host := grpcEndpoint
	for i := len(grpcEndpoint) - 1; i >= 0; i-- {
		if grpcEndpoint[i] == ':' {
			host = grpcEndpoint[:i]
			break
		}
	}
	return "https://" + host
}
