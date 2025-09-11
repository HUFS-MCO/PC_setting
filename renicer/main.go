package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ReniceRequest struct {
	ContainerID string `json:"container_id"`
	Nice        int    `json:"nice"`
}

type CgroupRequest struct {
	ContainerID string `json:"container_id"`
	Period      int    `json:"period"`
	Runtime     int    `json:"runtime"`
}

func handleRenice(w http.ResponseWriter, r *http.Request) {
	log.Println("Renice request received")
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Unable to read request body", http.StatusBadRequest)
		return
	}

	var req ReniceRequest
	err = json.Unmarshal(body, &req)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	containerID := req.ContainerID
	if strings.HasPrefix(containerID, "containerd://") {
		containerID = strings.TrimPrefix(containerID, "containerd://")
	}

	// Run crictl inspect to get PID
	inspectCmd := exec.Command("crictl", "inspect", containerID)
	inspectOutput, err := inspectCmd.Output()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to inspect container: %v", err), http.StatusInternalServerError)
		return
	}

	// Extract PID using jq
	jqCmd := exec.Command("jq", ".info.pid")
	jqCmd.Stdin = strings.NewReader(string(inspectOutput))
	pidBytes, err := jqCmd.Output()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to extract PID: %v", err), http.StatusInternalServerError)
		return
	}

	pidStr := strings.TrimSpace(string(pidBytes))

	// Run renice
	reniceCmd := exec.Command("renice", "-n", fmt.Sprintf("%d", req.Nice), "-p", pidStr)
	reniceOutput, err := reniceCmd.CombinedOutput()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to renice: %s", reniceOutput), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Renice applied successfully"))
}

func main() {
	socketPath := "/var/run/renicer.sock"
	//_ = exec.Command("rm", "-f", socketPath).Run()

	listener, err := net.Listen("tcp", "0.0.0.0:8080")
	if err != nil {
		log.Fatalf("Failed to listen on socket: %v", err)
	}
	defer listener.Close()

	log.Println("Renicer daemon listening on", socketPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/renice", handleRenice)
	mux.HandleFunc("/cgroup", handleCgroup)

	if err := http.Serve(listener, mux); err != nil {
		log.Fatalf("HTTP server error: %v", err)
	}
}

// applyCgroupFunc allows tests to mock cgroup application logic
var applyCgroupFunc = applyCgroup

func handleCgroup(w http.ResponseWriter, r *http.Request) {
	log.Println("Cgroup request received")
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Unable to read request body", http.StatusBadRequest)
		return
	}

	var req CgroupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.ContainerID == "" {
		http.Error(w, "container_id is required", http.StatusBadRequest)
		return
	}

	if req.Period <= 0 {
		http.Error(w, "period must be > 0 (microseconds)", http.StatusBadRequest)
		return
	}

	// runtime semantics: <0 disables; 0 allows no RT; 0<runtime<=period is valid
	if req.Runtime > req.Period {
		http.Error(w, "runtime must be <= period (or < 0 to disable)", http.StatusBadRequest)
		return
	}

	if err := applyCgroupFunc(req.ContainerID, req.Period, req.Runtime); err != nil {
		http.Error(w, fmt.Sprintf("Failed to update cgroup: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Cgroup RT settings applied successfully"))
}

func applyCgroup(containerID string, period int, runtime int) error {
	if strings.HasPrefix(containerID, "containerd://") {
		containerID = strings.TrimPrefix(containerID, "containerd://")
	}

	// Get PID via crictl + jq (to stay consistent with existing code path)
	inspectCmd := exec.Command("crictl", "inspect", containerID)
	inspectOutput, err := inspectCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to inspect container: %w", err)
	}
	jqCmd := exec.Command("jq", ".info.pid")
	jqCmd.Stdin = strings.NewReader(string(inspectOutput))
	pidBytes, err := jqCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to extract PID: %w", err)
	}
	pidStr := strings.TrimSpace(string(pidBytes))
	if pidStr == "" || pidStr == "null" {
		return fmt.Errorf("container PID not found")
	}

	// Determine cgroup version and path for the target PID
	cgroupV2, cgRelPath, err := detectCgroupForPid(pidStr)
	if err != nil {
		return err
	}

	if !cgroupV2 {
		return fmt.Errorf("cgroup v2 required but not detected")
	}

	// cgroup v2: write RT knobs
	cpuRTPeriodPath := filepath.Join("/sys/fs/cgroup", cgRelPath, "cpu.rt_period_us")
	cpuRTRuntimePath := filepath.Join("/sys/fs/cgroup", cgRelPath, "cpu.rt_runtime_us")

	if err := os.WriteFile(cpuRTPeriodPath, []byte(fmt.Sprintf("%d", period)), 0o644); err != nil {
		return fmt.Errorf("failed writing %s: %w", cpuRTPeriodPath, err)
	}
	// runtime <= 0 means disable RT runtime quota
	writeRuntime := runtime
	if writeRuntime < 0 {
		writeRuntime = 0
	}
	if err := os.WriteFile(cpuRTRuntimePath, []byte(fmt.Sprintf("%d", writeRuntime)), 0o644); err != nil {
		return fmt.Errorf("failed writing %s: %w", cpuRTRuntimePath, err)
	}
	return nil
}

// detectCgroupForPid returns whether the system is cgroup v2, and the relative cgroup path for the PID.
func detectCgroupForPid(pidStr string) (bool, string, error) {
	// Ensure cgroup v2 is available
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		return false, "", fmt.Errorf("cgroup v2 not detected: %w", err)
	}
	// Read unified cgroup path from /proc/<pid>/cgroup
	rel, err := readUnifiedCgroupPath(pidStr)
	if err != nil {
		return false, "", err
	}
	return true, rel, nil
}

func readUnifiedCgroupPath(pidStr string) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", pidStr, "cgroup"))
	if err != nil {
		return "", fmt.Errorf("read /proc/%s/cgroup: %w", pidStr, err)
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		// format v2: 0::/user.slice/…
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			return strings.TrimSpace(parts[2]), nil
		}
	}
	return "", fmt.Errorf("unified cgroup path not found for pid %s", pidStr)
}

// cgroup v1 helpers removed as we only support cgroup v2
