package stdlib

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lemonberrylabs/gcw-emulator/pkg/types"
)

// callbackContextKey is an unexported type for callback context keys.
type callbackContextKey string

const (
	// callbackBaseURLKey is the context key for the emulator base URL used in callback URLs.
	callbackBaseURLKey callbackContextKey = "callbackBaseURL"
	// callbackExecNameKey is the context key for the current execution name.
	callbackExecNameKey callbackContextKey = "callbackExecName"
)

// WithCallbackContext returns a context with callback URL generation info.
func WithCallbackContext(ctx context.Context, baseURL, execName string) context.Context {
	ctx = context.WithValue(ctx, callbackBaseURLKey, baseURL)
	ctx = context.WithValue(ctx, callbackExecNameKey, execName)
	return ctx
}

// CallbackStore manages pending callbacks for the emulator.
type CallbackStore struct {
	mu        sync.Mutex
	callbacks map[string]chan types.Value // callbackID -> channel
	metadata  map[string]*CallbackMeta   // callbackID -> metadata
	counter   int64
}

// CallbackMeta holds metadata about a callback for listing and URL resolution.
type CallbackMeta struct {
	ID        string
	URL       string
	Method    string
	ExecName  string
	CreatedAt time.Time
}

// globalCallbackStore is the singleton callback store.
var globalCallbackStore = &CallbackStore{
	callbacks: make(map[string]chan types.Value),
	metadata:  make(map[string]*CallbackMeta),
}

// GetCallbackStore returns the global callback store.
func GetCallbackStore() *CallbackStore {
	return globalCallbackStore
}

// Create creates a new callback and returns its ID and full URL.
func (s *CallbackStore) Create(baseURL, execName, method string) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	id := fmt.Sprintf("callback-%d", s.counter)
	s.callbacks[id] = make(chan types.Value, 1)

	callbackURL := fmt.Sprintf("%s/v1/%s/callbacks/%s", baseURL, execName, id)

	s.metadata[id] = &CallbackMeta{
		ID:        id,
		URL:       callbackURL,
		Method:    method,
		ExecName:  execName,
		CreatedAt: time.Now(),
	}

	return id, callbackURL
}

// Await waits for a callback to be triggered, times out, or is cancelled via context.
func (s *CallbackStore) Await(ctx context.Context, id string, timeout time.Duration) (types.Value, error) {
	s.mu.Lock()
	ch, ok := s.callbacks[id]
	s.mu.Unlock()

	if !ok {
		return types.Null, types.NewValueError(fmt.Sprintf("callback '%s' not found", id))
	}

	select {
	case val := <-ch:
		return val, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.callbacks, id)
		delete(s.metadata, id)
		s.mu.Unlock()
		return types.Null, ctx.Err()
	case <-time.After(timeout):
		s.mu.Lock()
		delete(s.callbacks, id)
		delete(s.metadata, id)
		s.mu.Unlock()
		return types.Null, types.NewTimeoutError("callback timed out")
	}
}

// Deliver sends data to a pending callback.
func (s *CallbackStore) Deliver(id string, data types.Value) error {
	s.mu.Lock()
	ch, ok := s.callbacks[id]
	s.mu.Unlock()

	if !ok {
		return fmt.Errorf("callback '%s' not found or already completed", id)
	}

	ch <- data
	return nil
}

// List returns all pending callback IDs.
func (s *CallbackStore) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.callbacks))
	for id := range s.callbacks {
		ids = append(ids, id)
	}
	return ids
}

// ListByExecution returns callback metadata for a given execution name.
func (s *CallbackStore) ListByExecution(execName string) []*CallbackMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []*CallbackMeta
	for _, meta := range s.metadata {
		if meta.ExecName == execName {
			result = append(result, meta)
		}
	}
	return result
}

// registerEvents registers events.* functions.
func (r *Registry) registerEvents() {
	r.Register("events.create_callback_endpoint", eventsCreateCallback)
	r.Register("events.await_callback", eventsAwaitCallback)
}

func eventsCreateCallback(ctx context.Context, args []types.Value) (types.Value, error) {
	// Extract HTTP method from args (default POST)
	method := "POST"
	if len(args) > 0 && args[0].Type() == types.TypeMap {
		m := args[0].AsMap()
		if meth, ok := m.Get("http_callback_method"); ok {
			method = meth.AsString()
		}
	}

	// Get base URL and execution name from context
	baseURL := "http://localhost:8787"
	if v, ok := ctx.Value(callbackBaseURLKey).(string); ok && v != "" {
		baseURL = v
	}
	execName := ""
	if v, ok := ctx.Value(callbackExecNameKey).(string); ok {
		execName = v
	}

	id, callbackURL := globalCallbackStore.Create(baseURL, execName, method)

	// Return callback info as a map — matching GCW format with url field
	m := types.NewOrderedMap()
	m.Set("callback_id", types.NewString(id))
	m.Set("url", types.NewString(callbackURL))
	return types.NewMap(m), nil
}

func eventsAwaitCallback(ctx context.Context, args []types.Value) (types.Value, error) {
	var callbackVal types.Value
	var timeoutSec float64 = 43200 // default 12 hours (GCW default)

	if len(args) > 0 && args[0].Type() == types.TypeMap {
		m := args[0].AsMap()
		if cb, ok := m.Get("callback"); ok {
			callbackVal = cb
		}
		if t, ok := m.Get("timeout"); ok {
			switch t.Type() {
			case types.TypeInt:
				timeoutSec = float64(t.AsInt())
			case types.TypeDouble:
				timeoutSec = t.AsDouble()
			}
		}
	}

	if callbackVal.IsNull() {
		return types.Null, types.NewValueError("events.await_callback: missing callback argument")
	}

	// Extract callback_id from the callback value
	var callbackID string
	if callbackVal.Type() == types.TypeMap {
		if id, ok := callbackVal.AsMap().Get("callback_id"); ok {
			callbackID = id.AsString()
		}
	}
	if callbackID == "" {
		return types.Null, types.NewValueError("events.await_callback: invalid callback value")
	}

	timeout := time.Duration(timeoutSec * float64(time.Second))
	return globalCallbackStore.Await(ctx, callbackID, timeout)
}
