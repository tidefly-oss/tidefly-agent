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
	"time"
)

const renewBeforeDays = 30

// startRenewalLoop checks the client cert daily and renews it if expiring soon.
// Call this as a goroutine after successful registration.
func (a *Agent) startRenewalLoop(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// Check immediately on start
	a.checkAndRenew(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.checkAndRenew(ctx)
		}
	}
}

func (a *Agent) checkAndRenew(ctx context.Context) {
	cert, err := loadCertFromFile(a.cfg.Plane.CertFile)
	if err != nil {
		slog.Warn("agent: renewal check — could not load cert", "error", err)
		return
	}

	daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
	slog.Debug("agent: cert expiry check", "days_left", daysLeft, "expires_at", cert.NotAfter)

	if daysLeft > renewBeforeDays {
		return
	}

	slog.Info("agent: cert expiring soon, renewing", "days_left", daysLeft)
	if err := a.renewCert(ctx); err != nil {
		slog.Error("agent: cert renewal failed", "error", err)
		return
	}
	slog.Info("agent: cert renewed successfully")
}

type renewRequest struct {
	WorkerID string `json:"worker_id"`
}

type renewResponse struct {
	CertPEM   string `json:"cert_pem"`
	KeyPEM    string `json:"key_pem"`
	CACertPEM string `json:"ca_cert_pem"`
	ExpiresAt string `json:"expires_at"`
}

func (a *Agent) renewCert(ctx context.Context) error {
	// Load current cert + key for mTLS on the renewal request itself
	tlsCert, err := tls.LoadX509KeyPair(a.cfg.Plane.CertFile, a.cfg.Plane.KeyFile)
	if err != nil {
		return fmt.Errorf("load current cert: %w", err)
	}

	caPEM, err := os.ReadFile(a.cfg.Plane.CAFile)
	if err != nil {
		return fmt.Errorf("read CA: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{tlsCert},
				RootCAs:      pool,
				MinVersion:   tls.VersionTLS13,
			},
		},
	}

	body, _ := json.Marshal(renewRequest{WorkerID: a.id})
	httpEndpoint := grpcToHTTP(a.cfg.Plane.Endpoint)
	url := httpEndpoint + "/api/v1/agent/renew"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("renewal request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("renewal failed (%d): %s", resp.StatusCode, string(b))
	}

	var renew renewResponse
	if err := json.NewDecoder(resp.Body).Decode(&renew); err != nil {
		return fmt.Errorf("decode renewal response: %w", err)
	}

	// Write new certs atomically (write to tmp, then rename)
	if err := writeFileAtomic(a.cfg.Plane.CertFile, []byte(renew.CertPEM), 0600); err != nil {
		return fmt.Errorf("write new cert: %w", err)
	}
	if err := writeFileAtomic(a.cfg.Plane.KeyFile, []byte(renew.KeyPEM), 0600); err != nil {
		return fmt.Errorf("write new key: %w", err)
	}
	if err := writeFileAtomic(a.cfg.Plane.CAFile, []byte(renew.CACertPEM), 0600); err != nil {
		return fmt.Errorf("write new CA: %w", err)
	}

	slog.Info("agent: new cert written", "expires_at", renew.ExpiresAt)
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadCertFromFile(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("decode PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}
