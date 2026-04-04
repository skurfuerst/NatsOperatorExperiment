package natsmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const connzPath = "/connz"

func newTestServer(t *testing.T, result *ConnzResult) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != connzPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}))
}

func TestGetConnz_Success(t *testing.T) {
	expected := &ConnzResult{
		ServerID:       "server-1",
		NumConnections: 2,
		Connections: []ConnectionInfo{
			{CID: 1, NKey: "UABC123", Account: "myaccount", IP: "10.0.0.1", Port: 4222},
			{CID: 2, NKey: "UDEF456", Account: "myaccount", IP: "10.0.0.2", Port: 4222},
		},
	}
	srv := newTestServer(t, expected)
	defer srv.Close()

	client := NewHTTPMonitorClient()
	result, err := client.GetConnz(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ServerID != "server-1" {
		t.Errorf("expected server_id 'server-1', got %q", result.ServerID)
	}
	if len(result.Connections) != 2 {
		t.Errorf("expected 2 connections, got %d", len(result.Connections))
	}
}

func TestGetConnz_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewHTTPMonitorClient()
	_, err := client.GetConnz(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestGetConnz_Unreachable(t *testing.T) {
	client := NewHTTPMonitorClient()
	_, err := client.GetConnz(context.Background(), "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

func TestQueryAllServers_MultiServer(t *testing.T) {
	srv1 := newTestServer(t, &ConnzResult{
		ServerID: "s1",
		Connections: []ConnectionInfo{
			{CID: 1, NKey: "UABC123", Account: "acct1"},
		},
	})
	defer srv1.Close()

	srv2 := newTestServer(t, &ConnzResult{
		ServerID: "s2",
		Connections: []ConnectionInfo{
			{CID: 10, NKey: "UABC123", Account: "acct1"},
			{CID: 11, NKey: "UDEF456", Account: "acct1"},
		},
	})
	defer srv2.Close()

	client := NewHTTPMonitorClient()
	urls := map[string]string{
		"nats-0": srv1.URL,
		"nats-1": srv2.URL,
	}
	results := QueryAllServers(context.Background(), client, urls)
	if len(results) != 2 {
		t.Fatalf("expected 2 server results, got %d", len(results))
	}

	totalConns := 0
	for _, r := range results {
		if r.Error != nil {
			t.Errorf("unexpected error for server %s: %v", r.ServerName, r.Error)
		}
		totalConns += len(r.Connections)
	}
	if totalConns != 3 {
		t.Errorf("expected 3 total connections, got %d", totalConns)
	}
}

func TestQueryAllServers_PartialFailure(t *testing.T) {
	srv1 := newTestServer(t, &ConnzResult{
		ServerID:    "s1",
		Connections: []ConnectionInfo{{CID: 1, NKey: "UABC123"}},
	})
	defer srv1.Close()

	urls := map[string]string{
		"nats-0": srv1.URL,
		"nats-1": "http://127.0.0.1:1", // unreachable
	}
	results := QueryAllServers(context.Background(), NewHTTPMonitorClient(), urls)

	var successes, failures int
	for _, r := range results {
		if r.Error != nil {
			failures++
		} else {
			successes++
		}
	}
	if successes != 1 || failures != 1 {
		t.Errorf("expected 1 success and 1 failure, got %d successes and %d failures", successes, failures)
	}
}

func TestFilterByNKey(t *testing.T) {
	servers := []ServerConnections{
		{
			ServerName: "nats-0",
			Connections: []ConnectionInfo{
				{CID: 1, NKey: "UABC123"},
				{CID: 2, NKey: "UDEF456"},
			},
		},
		{
			ServerName: "nats-1",
			Connections: []ConnectionInfo{
				{CID: 10, NKey: "UABC123"},
			},
		},
		{
			ServerName: "nats-2",
			Connections: []ConnectionInfo{
				{CID: 20, NKey: "UGHI789"},
			},
		},
	}

	filtered := FilterByNKey(servers, "UABC123")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 servers with matching connections, got %d", len(filtered))
	}
	totalConns := 0
	for _, s := range filtered {
		totalConns += len(s.Connections)
	}
	if totalConns != 2 {
		t.Errorf("expected 2 matching connections, got %d", totalConns)
	}
}

func TestFilterByNKey_NoMatch(t *testing.T) {
	servers := []ServerConnections{
		{
			ServerName:  "nats-0",
			Connections: []ConnectionInfo{{CID: 1, NKey: "UABC123"}},
		},
	}
	filtered := FilterByNKey(servers, "UNONEXISTENT")
	if len(filtered) != 0 {
		t.Errorf("expected 0 results, got %d", len(filtered))
	}
}

func TestFilterByAccount(t *testing.T) {
	servers := []ServerConnections{
		{
			ServerName: "nats-0",
			Connections: []ConnectionInfo{
				{CID: 1, Account: "acct1", NKey: "UABC123"},
				{CID: 2, Account: "acct2", NKey: "UDEF456"},
			},
		},
	}
	filtered := FilterByAccount(servers, "acct1")
	if len(filtered) != 1 {
		t.Fatalf("expected 1 server, got %d", len(filtered))
	}
	if len(filtered[0].Connections) != 1 {
		t.Errorf("expected 1 connection, got %d", len(filtered[0].Connections))
	}
	if filtered[0].Connections[0].NKey != "UABC123" {
		t.Errorf("expected NKey UABC123, got %s", filtered[0].Connections[0].NKey)
	}
}

func TestGetConnzClosed_Success(t *testing.T) {
	expected := &ConnzResult{
		ServerID:       "server-1",
		NumConnections: 2,
		Connections: []ConnectionInfo{
			{CID: 1, NKey: "UABC123", IP: "10.0.0.1", Port: 4222, Reason: "Authorization Violation", Stop: "2026-04-04T10:00:01Z"},
			{CID: 2, NKey: "UDEF456", IP: "10.0.0.2", Port: 4222, Reason: "Client Closed", Stop: "2026-04-04T10:00:02Z"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != connzPath {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("state") != "closed" {
			t.Errorf("expected state=closed query param, got %q", r.URL.Query().Get("state"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer srv.Close()

	client := NewHTTPMonitorClient()
	result, err := client.GetConnzClosed(context.Background(), srv.URL, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Connections) != 2 {
		t.Fatalf("expected 2 connections, got %d", len(result.Connections))
	}
	if result.Connections[0].Reason != "Authorization Violation" {
		t.Errorf("expected reason 'Authorization Violation', got %q", result.Connections[0].Reason)
	}
	if result.Connections[0].Stop != "2026-04-04T10:00:01Z" {
		t.Errorf("expected stop time, got %q", result.Connections[0].Stop)
	}
}

func TestQueryAllServersClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != connzPath {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(&ConnzResult{
			ServerID: "s1",
			Connections: []ConnectionInfo{
				{CID: 1, Reason: "Authorization Violation", Stop: "2026-04-04T10:00:01Z"},
			},
		})
	}))
	defer srv.Close()

	client := NewHTTPMonitorClient()
	urls := map[string]string{"nats-0": srv.URL}
	results := QueryAllServersClosed(context.Background(), client, urls, 50)
	if len(results) != 1 {
		t.Fatalf("expected 1 server result, got %d", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("unexpected error: %v", results[0].Error)
	}
	if len(results[0].Connections) != 1 {
		t.Fatalf("expected 1 closed connection, got %d", len(results[0].Connections))
	}
}

func TestFilterByNKey_PreservesErrors(t *testing.T) {
	servers := []ServerConnections{
		{
			ServerName:  "nats-0",
			Connections: []ConnectionInfo{{CID: 1, NKey: "UABC123"}},
		},
		{
			ServerName: "nats-1",
			Error:      fmt.Errorf("connection refused"),
		},
	}
	filtered := FilterByNKey(servers, "UABC123")
	if len(filtered) != 2 {
		t.Fatalf("expected 2 results (1 match + 1 error), got %d", len(filtered))
	}
}
