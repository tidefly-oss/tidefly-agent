package agent

import (
	"bufio"
	"log/slog"
	"os"
	"strings"
)

// clearRegistrationToken removes PLANE_TOKEN from the .env file after
// successful registration so it can't be reused or leaked.
func clearRegistrationToken(envFile string) {
	if envFile == "" {
		envFile = ".env"
	}

	input, err := os.ReadFile(envFile)
	if err != nil {
		slog.Warn("agent: could not read .env to clear token", "error", err)
		return
	}

	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(string(input)))
	for scanner.Scan() {
		line := scanner.Text()
		// Remove PLANE_TOKEN line entirely
		if strings.HasPrefix(strings.TrimSpace(line), "PLANE_TOKEN=") {
			lines = append(lines, "PLANE_TOKEN=")
			continue
		}
		lines = append(lines, line)
	}

	out := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(envFile, []byte(out), 0600); err != nil {
		slog.Warn("agent: could not clear PLANE_TOKEN from .env", "error", err)
		return
	}

	slog.Info("agent: PLANE_TOKEN cleared from .env")
}
