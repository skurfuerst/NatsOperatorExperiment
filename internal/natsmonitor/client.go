package natsmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// MonitorClient queries NATS monitoring endpoints.
type MonitorClient interface {
	GetConnz(ctx context.Context, baseURL string) (*ConnzResult, error)
}

// HTTPMonitorClient implements MonitorClient using HTTP.
type HTTPMonitorClient struct {
	client *http.Client
}

// NewHTTPMonitorClient creates a new HTTPMonitorClient with a 5s timeout.
func NewHTTPMonitorClient() *HTTPMonitorClient {
	return &HTTPMonitorClient{
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// GetConnz fetches the /connz endpoint from a NATS server.
func (c *HTTPMonitorClient) GetConnz(ctx context.Context, baseURL string) (*ConnzResult, error) {
	url := baseURL + "/connz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	var result ConnzResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response from %s: %w", url, err)
	}

	return &result, nil
}

// ServerConnections holds connections from a single NATS server.
type ServerConnections struct {
	ServerID    string
	ServerName  string
	Connections []ConnectionInfo
	Error       error
}

// QueryAllServers queries multiple NATS servers and aggregates results.
func QueryAllServers(ctx context.Context, client MonitorClient, serverURLs map[string]string) []ServerConnections {
	results := make([]ServerConnections, 0, len(serverURLs))
	for name, url := range serverURLs {
		connz, err := client.GetConnz(ctx, url)
		sc := ServerConnections{
			ServerName: name,
		}
		if err != nil {
			sc.Error = err
		} else {
			sc.ServerID = connz.ServerID
			sc.Connections = connz.Connections
		}
		results = append(results, sc)
	}
	return results
}

// FilterByNKey filters aggregated server connections to only those matching the given nkey.
func FilterByNKey(servers []ServerConnections, nkey string) []ServerConnections {
	var filtered []ServerConnections
	for _, s := range servers {
		if s.Error != nil {
			filtered = append(filtered, s)
			continue
		}
		var matching []ConnectionInfo
		for _, c := range s.Connections {
			if c.NKey == nkey {
				matching = append(matching, c)
			}
		}
		if len(matching) > 0 {
			filtered = append(filtered, ServerConnections{
				ServerID:    s.ServerID,
				ServerName:  s.ServerName,
				Connections: matching,
			})
		}
	}
	return filtered
}

// FilterByAccount filters aggregated server connections to only those matching the given account.
func FilterByAccount(servers []ServerConnections, account string) []ServerConnections {
	var filtered []ServerConnections
	for _, s := range servers {
		if s.Error != nil {
			filtered = append(filtered, s)
			continue
		}
		var matching []ConnectionInfo
		for _, c := range s.Connections {
			if c.Account == account {
				matching = append(matching, c)
			}
		}
		if len(matching) > 0 {
			filtered = append(filtered, ServerConnections{
				ServerID:    s.ServerID,
				ServerName:  s.ServerName,
				Connections: matching,
			})
		}
	}
	return filtered
}
