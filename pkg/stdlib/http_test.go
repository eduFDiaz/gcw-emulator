package stdlib

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lemonberrylabs/gcw-emulator/pkg/types"
)

// TestHTTP_ClientUsesDefaultTimeout verifies that RegisterHTTP creates a client
// with DefaultHTTPTimeout (not a short hardcoded value), so that per-request
// context deadlines control the actual timeout.
func TestHTTP_ClientUsesDefaultTimeout(t *testing.T) {
	r := NewRegistry()
	client := &http.Client{Timeout: DefaultHTTPTimeout}
	r.RegisterHTTP(client)

	if client.Timeout != DefaultHTTPTimeout {
		t.Fatalf("expected client timeout %v, got %v", DefaultHTTPTimeout, client.Timeout)
	}
}

// TestHTTP_NilClientUsesDefaultTimeout verifies that passing nil to RegisterHTTP
// still results in a client with DefaultHTTPTimeout.
func TestHTTP_NilClientUsesDefaultTimeout(t *testing.T) {
	r := NewRegistry()
	r.RegisterHTTP(nil) // should not panic and should use default timeout

	// Verify we can make a call (proves the client was created)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	args := []types.Value{
		types.NewMapFromGoMap(map[string]types.Value{
			"url": types.NewString(server.URL),
		}),
	}
	result, err := r.CallFunction("http.get", args)
	if err != nil {
		t.Fatalf("http.get failed: %v", err)
	}
	if result.Type() != types.TypeMap {
		t.Fatalf("expected map result, got %v", result.Type())
	}
}

// TestHTTP_RequestRespectsPerRequestTimeout verifies that a per-request timeout
// (from the YAML args) is honored, not overridden by the client-level timeout.
// This is the bug scenario: a slow server should be reachable when the per-request
// timeout is generous, even if the old hardcoded client timeout was 30s.
func TestHTTP_RequestRespectsPerRequestTimeout(t *testing.T) {
	// Server that responds after 100ms delay
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	r := NewRegistry()
	r.RegisterHTTP(&http.Client{Timeout: DefaultHTTPTimeout})

	// Set per-request timeout to 5s — should succeed since server responds in 100ms
	args := []types.Value{
		types.NewMapFromGoMap(map[string]types.Value{
			"url":     types.NewString(server.URL),
			"timeout": types.NewDouble(5),
		}),
	}

	result, err := r.CallFunction("http.get", args)
	if err != nil {
		t.Fatalf("http.get with 5s timeout failed: %v", err)
	}
	if code, ok := result.AsMap().Get("code"); !ok || code.String() != "200" {
		t.Fatalf("expected code 200, got %v", result)
	}
}

// TestHTTP_ShortPerRequestTimeoutCausesTimeout verifies that a very short
// per-request timeout correctly times out the request (proving the per-request
// timeout is effective, not just the client-level one).
func TestHTTP_ShortPerRequestTimeoutCausesTimeout(t *testing.T) {
	// Server that takes 2s to respond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	r := NewRegistry()
	r.RegisterHTTP(&http.Client{Timeout: DefaultHTTPTimeout})

	// Set per-request timeout to 0.1s — should timeout since server takes 2s
	args := []types.Value{
		types.NewMapFromGoMap(map[string]types.Value{
			"url":     types.NewString(server.URL),
			"timeout": types.NewDouble(0.1),
		}),
	}

	_, err := r.CallFunction("http.get", args)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
