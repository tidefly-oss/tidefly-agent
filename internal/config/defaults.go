package config

import "github.com/spf13/viper"

func setDefaults() {
	viper.SetDefault("AGENT_ID", "")
	viper.SetDefault("AGENT_NAME", "")
	viper.SetDefault("AGENT_VERSION", "0.1.0")

	viper.SetDefault("PLANE_ENDPOINT", "")
	viper.SetDefault("PLANE_TOKEN", "")
	viper.SetDefault("PLANE_CERT_FILE", "/etc/tidefly-agent/client.crt")
	viper.SetDefault("PLANE_KEY_FILE", "/etc/tidefly-agent/client.key")
	viper.SetDefault("PLANE_CA_FILE", "/etc/tidefly-agent/ca.crt")
	viper.SetDefault("PLANE_DIAL_TIMEOUT", "10s")
	viper.SetDefault("PLANE_RETRY_DELAY", "5s")

	viper.SetDefault("RUNTIME_TYPE", "docker")
	viper.SetDefault("RUNTIME_SOCKET", "/var/run/docker.sock")

	viper.SetDefault("CADDY_ENABLED", true)
	viper.SetDefault("CADDY_ADMIN_URL", "http://127.0.0.1:2019")

	viper.SetDefault("LOG_LEVEL", "info")
	viper.SetDefault("AGENT_ENV_FILE", "/etc/tidefly-agent/.env")
}
