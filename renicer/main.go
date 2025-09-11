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

	// Determine cgroup version
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		return fmt.Errorf("cgroup v2 not detected: %w", err)
	}

	// Resolve cgroup absolute paths (container and pod) from crictl inspect
	containerCgPath, podCgPath, err := getCgroupPathsFromInspect(containerID)
	if err != nil {
		return err
	}

	if !cgroupV2 {
		return fmt.Errorf("cgroup v2 required but not detected")
	}

	// Preflight checks for cgroup v2 RT
	// 1) cpu controller must be available at root and enabled in parent subtree
	rootControllers, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
	if err != nil {
		return fmt.Errorf("read cgroup.controllers: %w", err)
	}
	if !tokenContains(string(rootControllers), "cpu") {
		return fmt.Errorf("cpu controller not available in cgroup v2 (cgroup.controllers)" )
	}
	parentDir := filepath.Dir(containerCgPath)
	subtreeCtl, err := os.ReadFile(filepath.Join(parentDir, "cgroup.subtree_control"))
	if err == nil { // some leaves may not have this; ignore if missing
		if !tokenContains(string(subtreeCtl), "+cpu") && !tokenContains(string(subtreeCtl), "cpu") {
			return fmt.Errorf("cpu controller not enabled in parent cgroup (cgroup.subtree_control)" )
		}
	}

	// 2) system-wide RT group scheduling must be enabled and limits respected
	globalRTPeriod, err := readIntFromFile("/proc/sys/kernel/sched_rt_period_us")
	if err != nil {
		return fmt.Errorf("read sched_rt_period_us: %w", err)
	}
	globalRTRuntime, err := readIntFromFile("/proc/sys/kernel/sched_rt_runtime_us")
	if err != nil {
		return fmt.Errorf("read sched_rt_runtime_us: %w", err)
	}
	if globalRTRuntime < 0 {
		return fmt.Errorf("system RT throttling is disabled (sched_rt_runtime_us = -1); per-cgroup RT runtime cannot be set")
	}
	if period <= 0 || globalRTPeriod <= 0 {
		return fmt.Errorf("invalid period (input=%d, system=%d)", period, globalRTPeriod)
	}

	// 3) parent cgroup RT limits: child cannot exceed ancestor limits in v2
	parentRTPeriod, parentRTRuntime, parentPath, err := getParentRTLimits(containerCgPath)
	if err == nil { // if found, normalize against parent
		// Use parent's period to avoid EINVAL due to mismatch
		if parentRTPeriod > 0 {
			period = parentRTPeriod
		}
		// Cap runtime to parent's runtime if parent enforces it (>0). 0 means no RT allowed
		if parentRTRuntime == 0 && runtime > 0 {
			return fmt.Errorf("parent cgroup (%s) disallows RT runtime (0)", parentPath)
		}
		if runtime > 0 && parentRTRuntime > 0 && runtime > parentRTRuntime {
			runtime = parentRTRuntime
		}
	}

	// cgroup v2: write RT knobs to both container and pod cgroups
	for _, cgPath := range []string{containerCgPath, podCgPath} {
		cpuRTPeriodPath := filepath.Join(cgPath, "cpu.rt_period_us")
		cpuRTRuntimePath := filepath.Join(cgPath, "cpu.rt_runtime_us")
		if err := os.WriteFile(cpuRTPeriodPath, []byte(fmt.Sprintf("%d", period)), 0o644); err != nil {
			return fmt.Errorf("failed writing %s: %w", cpuRTPeriodPath, err)
		}
		writeRuntime := runtime
		if writeRuntime < 0 {
			writeRuntime = 0
		}
		if err := os.WriteFile(cpuRTRuntimePath, []byte(fmt.Sprintf("%d", writeRuntime)), 0o644); err != nil {
			return fmt.Errorf("failed writing %s: %w", cpuRTRuntimePath, err)
		}
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

// helpers
func readIntFromFile(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(b))
	var v int
	_, err = fmt.Sscanf(s, "%d", &v)
	if err != nil {
		return 0, fmt.Errorf("parse int from %s: %w", path, err)
	}
	return v, nil
}

func tokenContains(s string, want string) bool {
	for _, t := range strings.Fields(s) {
		if t == want {
			return true
		}
	}
	return false
}

// getCgroupPathsFromInspect builds absolute cgroup v2 paths for container and its pod from crictl inspect
func getCgroupPathsFromInspect(containerID string) (string, string, error) {
	inspectCmd := exec.Command("crictl", "inspect", containerID)
	inspectOutput, err := inspectCmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("failed to inspect container: %w", err)
	}
	// Try to extract cgroupsPath via jq searching any object with cgroupsPath field
	jqCmd := exec.Command("jq", "-r", ".. | select(type==\"object\" and has(\"cgroupsPath\")) | .cgroupsPath | select(.!=null) | .")
	jqCmd.Stdin = strings.NewReader(string(inspectOutput))
	cgroupsPathBytes, err := jqCmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("failed to extract cgroupsPath: %w", err)
	}
	cgroupsPath := strings.TrimSpace(string(cgroupsPathBytes))
	if cgroupsPath == "" {
		return "", "", fmt.Errorf("cgroupsPath not found in inspect output")
	}
	// Expected form: "<sliceGroup>:<runtime>:<scopeId>"
	parts := strings.Split(cgroupsPath, ":")
	if len(parts) != 3 {
		return "", "", fmt.Errorf("unexpected cgroupsPath format: %s", cgroupsPath)
	}
	sliceGroup := parts[0]                             // e.g. kubepods-besteffort-podXXXX.slice
	runtimeName := parts[1]                            // e.g. cri-containerd
	scopeID := parts[2]                                // e.g. <container-id>
	// Derive QoS slice (segment before -pod) and pod slice
	idx := strings.LastIndex(sliceGroup, "-pod")
	if idx <= 0 {
		return "", "", fmt.Errorf("cannot locate pod segment in slice: %s", sliceGroup)
	}
	qosSlice := sliceGroup[:idx] + ".slice"           // e.g. kubepods-besteffort.slice
	podSlice := sliceGroup                              // full pod slice name (already ends with .slice)
	// Build absolute pod path under unified hierarchy
	podPath := filepath.Join("/sys/fs/cgroup", "kubepods.slice", qosSlice, podSlice)
	containerScope := runtimeName + "-" + scopeID + ".scope"
	containerPath := filepath.Join(podPath, containerScope)
	return containerPath, podPath, nil
}

// getParentRTLimits walks up from a cgroup path to find the nearest ancestor
// that has cpu.rt_period_us and cpu.rt_runtime_us, returning their values.
func getParentRTLimits(absCgPath string) (int, int, string, error) {
	cur := filepath.Clean(absCgPath)
	for {
		parent := filepath.Dir(cur)
		if parent == cur || parent == "/" {
			return 0, 0, "", fmt.Errorf("no parent RT limits found")
		}
		pPeriod := filepath.Join(parent, "cpu.rt_period_us")
		pRuntime := filepath.Join(parent, "cpu.rt_runtime_us")
		p1, err1 := readIntFromFile(pPeriod)
		p2, err2 := readIntFromFile(pRuntime)
		if err1 == nil && err2 == nil {
			return p1, p2, parent, nil
		}
		cur = parent
	}
}
