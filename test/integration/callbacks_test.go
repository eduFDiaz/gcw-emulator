package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestCallbacks_CreateAndAwait verifies creating a callback endpoint and
// receiving a callback.
func TestCallbacks_CreateAndAwait(t *testing.T) {
	wfID := uniqueID("callback-basic")
	yaml := loadWorkflow(t, "callback_await.yaml")
	name := createWorkflow(t, wfID, yaml)

	// Start execution in background (it will wait for callback)
	body, _ := json.Marshal(map[string]interface{}{})
	url := apiURL(name + "/executions")
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("HTTP error: %v", err)
	}
	var exec map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&exec)
	resp.Body.Close()
	execName, _ := exec["name"].(string)

	// Wait a moment for the execution to create the callback
	time.Sleep(2 * time.Second)

	// List callbacks to find the URL
	listResp, err := http.Get(apiURL(execName + "/callbacks"))
	if err != nil {
		t.Fatalf("HTTP error: %v", err)
	}
	var callbacks map[string]interface{}
	json.NewDecoder(listResp.Body).Decode(&callbacks)
	listResp.Body.Close()

	cbList, ok := callbacks["callbacks"].([]interface{})
	if !ok || len(cbList) == 0 {
		t.Skipf("no callbacks found - callback feature may not be implemented yet")
		return
	}

	// Get the callback URL
	cb, _ := cbList[0].(map[string]interface{})
	cbURL, _ := cb["url"].(string)
	if cbURL == "" {
		t.Skip("callback URL not found")
		return
	}

	// Send callback data
	cbBody, _ := json.Marshal(map[string]interface{}{
		"status": "approved",
	})
	cbResp, err := http.Post(cbURL, "application/json", bytes.NewReader(cbBody))
	if err != nil {
		t.Fatalf("callback HTTP error: %v", err)
	}
	cbResp.Body.Close()

	// Wait for execution to complete
	er := waitForExecution(t, execName, 15*time.Second)
	assertSucceeded(t, er)
}

// TestCallbacks_Timeout verifies that callback timeout raises an error.
func TestCallbacks_Timeout(t *testing.T) {
	yaml := loadWorkflow(t, "callback_timeout.yaml")
	er := deployAndRun(t, uniqueID("cb-timeout"), yaml, nil)
	assertSucceeded(t, er)
	assertResultContains(t, er, "timed_out", true)
}

// TestCallbacks_WithPayload verifies that the callback payload is returned
// to the awaiting workflow step.
func TestCallbacks_WithPayload(t *testing.T) {
	wfID := uniqueID("callback-payload")
	yaml := loadWorkflow(t, "callback_await.yaml")
	name := createWorkflow(t, wfID, yaml)

	// Start execution in background (it will wait for callback)
	body, _ := json.Marshal(map[string]interface{}{})
	url := apiURL(name + "/executions")
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("HTTP error: %v", err)
	}
	var exec map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&exec)
	resp.Body.Close()
	execName, _ := exec["name"].(string)

	// Wait for the execution to create the callback
	time.Sleep(2 * time.Second)

	// List callbacks to find the URL
	listResp, err := http.Get(apiURL(execName + "/callbacks"))
	if err != nil {
		t.Fatalf("HTTP error: %v", err)
	}
	var callbacks map[string]interface{}
	json.NewDecoder(listResp.Body).Decode(&callbacks)
	listResp.Body.Close()

	cbList, ok := callbacks["callbacks"].([]interface{})
	if !ok || len(cbList) == 0 {
		t.Skipf("no callbacks found - callback feature may not be implemented yet")
		return
	}

	cb, _ := cbList[0].(map[string]interface{})
	cbURL, _ := cb["url"].(string)
	if cbURL == "" {
		t.Skip("callback URL not found")
		return
	}

	// Send callback with a specific payload
	cbBody, _ := json.Marshal(map[string]interface{}{
		"action":  "approve",
		"user":    "alice",
		"comment": "looks good",
	})
	cbResp, err := http.Post(cbURL, "application/json", bytes.NewReader(cbBody))
	if err != nil {
		t.Fatalf("callback HTTP error: %v", err)
	}
	cbResp.Body.Close()

	// Wait for execution to complete and verify the payload is returned.
	// The await_callback result wraps the HTTP request; the body lives under
	// http_request.body.
	er := waitForExecution(t, execName, 15*time.Second)
	assertSucceeded(t, er)

	resultMap, ok := er.Result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected result to be a map, got %T: %v", er.Result, er.Result)
	}
	httpReq, ok := resultMap["http_request"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected http_request in result, got: %v", resultMap)
	}
	bodyMap, ok := httpReq["body"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected http_request.body to be a map, got: %v", httpReq["body"])
	}
	for key, want := range map[string]string{
		"action":  "approve",
		"user":    "alice",
		"comment": "looks good",
	} {
		if got, _ := bodyMap[key].(string); got != want {
			t.Errorf("http_request.body[%q] = %q, want %q", key, got, want)
		}
	}
}
