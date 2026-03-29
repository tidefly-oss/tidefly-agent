package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type CaddyClient struct {
	adminURL string
	http     *http.Client
}

func NewCaddyClient(adminURL string) *CaddyClient {
	return &CaddyClient{
		adminURL: adminURL,
		http:     &http.Client{},
	}
}

func (c *CaddyClient) RegisterRoute(ctx context.Context, upstream, domain string, tls bool) error {
	route := buildRoute(upstream, domain, tls)
	body, err := json.Marshal(route)
	if err != nil {
		return err
	}

	url := c.adminURL + "/config/apps/http/servers/tidefly/routes"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("caddy register route: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("caddy register route: status %d", resp.StatusCode)
	}
	return nil
}

func (c *CaddyClient) RemoveRoute(ctx context.Context, domain string) error {
	url := fmt.Sprintf("%s/config/apps/http/servers/tidefly/routes/%s", c.adminURL, domain)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("caddy remove route: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func buildRoute(upstream, domain string, _ bool) map[string]any {
	return map[string]any{
		"@id": domain,
		"match": []map[string]any{
			{"host": []string{domain}},
		},
		"handle": []map[string]any{
			{
				"handler": "reverse_proxy",
				"upstreams": []map[string]any{
					{"dial": upstream},
				},
			},
		},
		"terminal": true,
	}
}
