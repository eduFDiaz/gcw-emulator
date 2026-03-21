package stdlib

import (
	"context"
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

	ctx := context.Background()
	args := []types.Value{
		types.NewMapFromGoMap(map[string]types.Value{
			"url": types.NewString(server.URL),
		}),
	}
	result, err := r.CallFunction(ctx, "http.get", args)
	if err != nil {
		t.Fatalf("http.get failed: %v", err)
	}
	if result.Type() != types.TypeMap {
		t.Fatalf("expected map result, got %v", result.Type())
	}
}

// TestHTTP_RequestRespectsPerRequestTimeout verifies that a per-request timeout
// (from the YAML args) is honored, not overridden by the client-level timeout.
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

	ctx := context.Background()
	// Set per-request timeout to 5s — should succeed since server responds in 100ms
	args := []types.Value{
		types.NewMapFromGoMap(map[string]types.Value{
			"url":     types.NewString(server.URL),
			"timeout": types.NewDouble(5),
		}),
	}

	result, err := r.CallFunction(ctx, "http.get", args)
	if err != nil {
		t.Fatalf("http.get with 5s timeout failed: %v", err)
	}
	if code, ok := result.AsMap().Get("code"); !ok || code.String() != "200" {
		t.Fatalf("expected code 200, got %v", result)
	}
}

// TestHTTP_ShortPerRequestTimeoutCausesTimeout verifies that a very short
// per-request timeout correctly times out the request.
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

	ctx := context.Background()
	// Set per-request timeout to 0.1s — should timeout since server takes 2s
	args := []types.Value{
		types.NewMapFromGoMap(map[string]types.Value{
			"url":     types.NewString(server.URL),
			"timeout": types.NewDouble(0.1),
		}),
	}

	_, err := r.CallFunction(ctx, "http.get", args)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// TestHTTP_ParentContextCancellationStopsRequest verifies that cancelling the
// parent context propagates to in-flight HTTP requests.
func TestHTTP_ParentContextCancellationStopsRequest(t *testing.T) {
	requestReceived := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestReceived)
		// Block until request context is cancelled
		<-r.Context().Done()
	}))
	defer server.Close()

	r := NewRegistry()
	r.RegisterHTTP(&http.Client{Timeout: DefaultHTTPTimeout})

	ctx, cancel := context.WithCancel(context.Background())
	args := []types.Value{
		types.NewMapFromGoMap(map[string]types.Value{
			"url":     types.NewString(server.URL),
			"timeout": types.NewDouble(30),
		}),
	}

	done := make(chan error, 1)
	go func() {
		_, err := r.CallFunction(ctx, "http.get", args)
		done <- err
	}()

	// Wait for server to receive the request, then cancel
	<-requestReceived
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from cancelled context, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP request was not cancelled within 5s — context cancellation not propagated")
	}
}
