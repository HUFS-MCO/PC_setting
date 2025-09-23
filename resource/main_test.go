package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCgroupHandler_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/cgroup", nil)
	rr := httptest.NewRecorder()

	handleCgroup(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}

func TestCgroupHandler_BadJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/cgroup", bytes.NewBufferString("{"))
	rr := httptest.NewRecorder()

	handleCgroup(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestCgroupHandler_MissingContainerID(t *testing.T) {
	body := map[string]any{
		"period":  100000,
		"runtime": 5000,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/cgroup", bytes.NewReader(b))
	rr := httptest.NewRecorder()

	handleCgroup(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestCgroupHandler_InvalidPeriod(t *testing.T) {
	body := map[string]any{
		"container_id": "abc",
		"period":       0,
		"runtime":      1000,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/cgroup", bytes.NewReader(b))
	rr := httptest.NewRecorder()

	handleCgroup(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestCgroupHandler_RuntimeGreaterThanPeriod(t *testing.T) {
	body := map[string]any{
		"container_id": "abc",
		"period":       1000,
		"runtime":      1001,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/cgroup", bytes.NewReader(b))
	rr := httptest.NewRecorder()

	handleCgroup(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestCgroupHandler_Success(t *testing.T) {
	// Mock the apply function to avoid touching real cgroups
	orig := applyCgroupFunc
	applyCgroupFunc = func(containerID string, period int, runtime int) error { return nil }
	defer func() { applyCgroupFunc = orig }()

	body := map[string]any{
		"container_id": "abc",
		"period":       100000,
		"runtime":      5000,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/cgroup", bytes.NewReader(b))
	rr := httptest.NewRecorder()

	handleCgroup(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rr.Code)
	}
}

func TestCgroupHandler_DisableRuntime(t *testing.T) {
	// runtime < 0 should be accepted and passed through
	orig := applyCgroupFunc
	var captured struct{
		containerID string
		period int
		runtime int
	}
	applyCgroupFunc = func(containerID string, period int, runtime int) error {
		captured.containerID = containerID
		captured.period = period
		captured.runtime = runtime
		return nil
	}
	defer func() { applyCgroupFunc = orig }()

	body := map[string]any{
		"container_id": "abc",
		"period":       100000,
		"runtime":      -1,
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/cgroup", bytes.NewReader(b))
	rr := httptest.NewRecorder()

	handleCgroup(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rr.Code)
	}
	if captured.runtime != -1 {
		t.Fatalf("expected runtime -1 to pass through, got %d", captured.runtime)
	}
}


