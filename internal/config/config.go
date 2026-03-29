package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

func Load() (*Config, error) {
	viper.SetConfigFile(".env")
	viper.SetConfigType("env")
	_ = viper.ReadInConfig()

	viper.AutomaticEnv()
	setDefaults()

	cfg := parse()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parse() *Config {
	dialTimeout, _ := time.ParseDuration(viper.GetString("PLANE_DIAL_TIMEOUT"))
	if dialTimeout == 0 {
		dialTimeout = 10 * time.Second
	}
	retryDelay, _ := time.ParseDuration(viper.GetString("PLANE_RETRY_DELAY"))
	if retryDelay == 0 {
		retryDelay = 5 * time.Second
	}

	name := viper.GetString("AGENT_NAME")
	if name == "" {
		name, _ = os.Hostname()
	}

	return &Config{
		Agent: AgentConfig{
			ID:      viper.GetString("AGENT_ID"),
			Name:    name,
			Version: viper.GetString("AGENT_VERSION"),
			EnvFile: viper.GetString("AGENT_ENV_FILE"),
		},
		Plane: PlaneConfig{
			Endpoint:    viper.GetString("PLANE_ENDPOINT"),
			Token:       viper.GetString("PLANE_TOKEN"),
			CertFile:    viper.GetString("PLANE_CERT_FILE"),
			KeyFile:     viper.GetString("PLANE_KEY_FILE"),
			CAFile:      viper.GetString("PLANE_CA_FILE"),
			DialTimeout: dialTimeout,
			RetryDelay:  retryDelay,
		},
		Runtime: RuntimeConfig{
			Type:       viper.GetString("RUNTIME_TYPE"),
			SocketPath: viper.GetString("RUNTIME_SOCKET"),
		},
		Caddy: CaddyConfig{
			Enabled:  viper.GetBool("CADDY_ENABLED"),
			AdminURL: viper.GetString("CADDY_ADMIN_URL"),
		},
		Logger: LoggerConfig{
			Level: viper.GetString("LOG_LEVEL"),
		},
	}
}

func (c *Config) validate() error {
	var errs []string

	if c.Plane.Endpoint == "" {
		errs = append(errs, "PLANE_ENDPOINT is required (e.g. plane.example.com:7443)")
	}

	// Token only required if no cert exists yet (first-time registration)
	certExists := fileExists(c.Plane.CertFile)
	if !certExists && c.Plane.Token == "" {
		errs = append(errs, "PLANE_TOKEN is required for first-time registration (generate one in the Tidefly UI)")
	}

	if c.Runtime.SocketPath == "" {
		errs = append(errs, "RUNTIME_SOCKET is required")
	}

	if len(errs) > 0 {
		return errors.New("config validation failed:\n  - " + strings.Join(errs, "\n  - "))
	}
	return nil
}

// IsRegistered returns true if the agent already has a client certificate.
func (c *Config) IsRegistered() bool {
	return fileExists(c.Plane.CertFile) && fileExists(c.Plane.KeyFile)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// CertDir returns the directory where certs are stored.
func (c *Config) CertDir() string {
	parts := strings.Split(c.Plane.CertFile, "/")
	if len(parts) <= 1 {
		return "."
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

func (c *Config) String() string {
	return fmt.Sprintf(
		"agent=%s endpoint=%s runtime=%s",
		c.Agent.Name, c.Plane.Endpoint, c.Runtime.Type,
	)
}
