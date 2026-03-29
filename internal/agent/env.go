package agent

import (
	"bufio"
	"log/slog"
	"os"
	"strings"
)

// patchEnvAfterRegistration schreibt nach erfolgreicher Registration:
//   - PLANE_TOKEN → leer (one-time, darf nicht liegen bleiben)
//   - AGENT_ID   → persistieren falls noch leer
func patchEnvAfterRegistration(envFile, agentID string) {
	lines, err := readEnvLines(envFile)
	if err != nil {
		slog.Warn("agent: could not read .env for patching", "file", envFile, "error", err)
		return
	}

	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "PLANE_TOKEN="):
			out = append(out, "PLANE_TOKEN=")
		case strings.HasPrefix(trimmed, "AGENT_ID=") && strings.TrimPrefix(trimmed, "AGENT_ID=") == "":
			out = append(out, "AGENT_ID="+agentID)
		default:
			out = append(out, line)
		}
	}

	if err := os.WriteFile(envFile, []byte(strings.Join(out, "\n")+"\n"), 0600); err != nil {
		slog.Warn("agent: could not patch .env", "file", envFile, "error", err)
		return
	}
	slog.Info("agent: .env patched (token cleared, agent ID persisted)", "file", envFile)
}

func readEnvLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}
