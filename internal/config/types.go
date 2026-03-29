package config

import "time"

type Config struct {
	Agent   AgentConfig
	Plane   PlaneConfig
	Runtime RuntimeConfig
	Caddy   CaddyConfig
	Logger  LoggerConfig
}

type AgentConfig struct {
	ID      string // UUID — generated on first start, persisted
	Name    string // human-readable hostname
	Version string
	EnvFile string
}

type PlaneConfig struct {
	Endpoint    string // plane gRPC endpoint, e.g. plane.example.com:7443
	Token       string // one-time registration token (tfy_reg_...)
	CertFile    string // path to store issued client cert
	KeyFile     string // path to store issued client key
	CAFile      string // path to store plane CA cert
	DialTimeout time.Duration
	RetryDelay  time.Duration
}

type RuntimeConfig struct {
	Type       string // "docker" | "podman"
	SocketPath string
}

type CaddyConfig struct {
	Enabled  bool
	AdminURL string
}

type LoggerConfig struct {
	Level string
}
