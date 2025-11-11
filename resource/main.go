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
	"strconv"
	"path/filepath"
	"strings"
)

// Cgroup file paths and constants
const (
	cgroupV2Root              = "/sys/fs/cgroup"
	cgroupControllersFile     = cgroupV2Root + "/cgroup.controllers"
	cgroupKubepodsSlice       = cgroupV2Root + "/kubepods.slice"
	
	// RT scheduling sysctl paths
	procSchedRTPeriod         = "/proc/sys/kernel/sched_rt_period_us"
	procSchedRTRuntime        = "/proc/sys/kernel/sched_rt_runtime_us"
	
	// Cgroup control file names (relative to cgroup path)
	cgroupSubtreeControl      = "cgroup.subtree_control"
	cgroupRTPeriod            = "cpu.rt_period_us"
	cgroupRTRuntime           = "cpu.rt_runtime_us"
	cgroupRTMultiRuntime      = "cpu.rt_multi_runtime_us"
	cgroupCPUSet              = "cpuset.cpus"
)

type ReniceRequest struct {
	ContainerID string `json:"container_id"`
	Nice        int    `json:"nice"`
}

type CgroupRequest struct {
	ContainerID string  `json:"container_id"`
	Period      int     `json:"period"`
	Runtime     int     `json:"runtime"`
	Core        *string `json:"core,omitempty"`
	OnlyRuntime bool    `json:"only_runtime,omitempty"` // true = escalation 모드 (period 변경 안함)
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
	listener, err := net.Listen("tcp", "0.0.0.0:8080")
	if err != nil {
		log.Fatalf("Failed to listen on socket: %v", err)
	}
	defer listener.Close()

	log.Printf("Resource Controller listening on port %d", 8080)

	mux := http.NewServeMux()
	mux.HandleFunc("/renice", handleRenice)
	mux.HandleFunc("/cgroup", handleCgroup)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

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

	// Core value is now handled as a string range, no validation needed here

	if err := applyCgroupFunc(req.ContainerID, req.Period, req.Runtime, req.Core, req.OnlyRuntime); err != nil {
		http.Error(w, fmt.Sprintf("Failed to update cgroup: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Cgroup RT settings and CPU set applied successfully"))
}

func applyCgroup(containerID string, period int, runtime int, core *string, onlyRuntime bool) error {
	if strings.HasPrefix(containerID, "containerd://") {
		containerID = strings.TrimPrefix(containerID, "containerd://")
	}

	// Determine cgroup version
	if _, err := os.Stat(cgroupControllersFile); err != nil {
		return fmt.Errorf("cgroup v2 not detected: %w", err)
	}

	// Resolve cgroup absolute paths (container and pod) from crictl inspect
	containerCgPath, podCgPath, err := getCgroupPathsFromInspect(containerID)
	if err != nil {
		return fmt.Errorf("failed to get cgroup paths: %w", err)
	}

	// Preflight checks for cgroup v2 RT
	// 1) cpu controller must be available at root and enabled in parent subtree
	rootControllers, err := os.ReadFile(cgroupControllersFile)
	if err != nil {
		return fmt.Errorf("read cgroup.controllers: %w", err)
	}
	if !tokenContains(string(rootControllers), "cpu") {
		return fmt.Errorf("cpu controller not available in cgroup v2 (cgroup.controllers)")
	}
	// 2) system-wide RT group scheduling must be enabled and limits respected
	globalRTPeriod, err := readIntFromFile(procSchedRTPeriod)
	if err != nil {
		return fmt.Errorf("read sched_rt_period_us: %w", err)
	}
	globalRTRuntime, err := readIntFromFile(procSchedRTRuntime)
	if err != nil {
		return fmt.Errorf("read sched_rt_runtime_us: %w", err)
	}
	if globalRTRuntime < 0 {
		return fmt.Errorf("system RT throttling is disabled (sched_rt_runtime_us = -1); per-cgroup RT runtime cannot be set")
	}
	if period <= 0 || globalRTPeriod <= 0 {
		return fmt.Errorf("invalid period (input=%d, system=%d)", period, globalRTPeriod)
	}

	// Check parent RT limits first
	rootPeriod, err := readIntFromFile(filepath.Join(cgroupKubepodsSlice, cgroupRTPeriod))
	if err != nil {
		return fmt.Errorf("failed to read root RT period: %w", err)
	}
	
	if rootPeriod != 0 && period > rootPeriod {
		return fmt.Errorf("requested period %d exceeds root limit %d", period, rootPeriod)
	}

	// Read current values
	podPeriod, podRuntime, err := readRtValues(podCgPath)
	if err != nil {
		return fmt.Errorf("failed to read current pod RT values: %w", err)
	}

	var podMultiRuntime int
	if core != nil {
		podMultiRuntime = getCurrentMultiRuntime(podCgPath, *core)
	}

	// Pre-calculate decrease flags for all scenarios
	runtimeDecreasing := isRuntimeDecrease(podRuntime, runtime)
	periodDecreasing := isDecrease(podPeriod, period)
	multiRuntimeDecreasing := core != nil && isRuntimeDecrease(podMultiRuntime, runtime)

	// Determine if we're in escalation mode (only runtime change)
	if onlyRuntime {
		log.Printf("Escalation mode: updating only runtime (core: %v, runtime: %d)", core, runtime)
		
		// Use appropriate decrease flag based on core setting
		decreasing := runtimeDecreasing
		if core != nil {
			decreasing = multiRuntimeDecreasing
		}
		
		return applyRuntimeWithOrder(containerCgPath, podCgPath, runtime, core, decreasing)
	}

	// Normal mode: combine all decrease flags
	decreasing := runtimeDecreasing || periodDecreasing || multiRuntimeDecreasing

	if decreasing {
		// For decrease operations, write container (child) first, then pod (parent)
		log.Printf("Decreasing limits: container first (runtime: %v, period: %v, core: %v)",
			runtimeDecreasing, periodDecreasing, multiRuntimeDecreasing)
		if err := writeRtValues(containerCgPath, period, runtime, core, periodDecreasing, runtimeDecreasing, multiRuntimeDecreasing); err != nil {
			return fmt.Errorf("failed to update container cgroup RT: %w", err)
		}
		if err := writeRtValues(podCgPath, period, runtime, core, periodDecreasing, runtimeDecreasing, multiRuntimeDecreasing); err != nil {
			return fmt.Errorf("failed to update pod cgroup RT: %w", err)
		}
	} else {
		// When increasing, write pod (parent) first, then container (child)
		log.Printf("Increasing limits: pod first (runtime: %v, period: %v, core: %v)",
			!runtimeDecreasing, !periodDecreasing, !multiRuntimeDecreasing)
		if err := writeRtValues(podCgPath, period, runtime, core, periodDecreasing, runtimeDecreasing, multiRuntimeDecreasing); err != nil {
			return fmt.Errorf("failed to update pod cgroup RT: %w", err)
		}
		if err := writeRtValues(containerCgPath, period, runtime, core, periodDecreasing, runtimeDecreasing, multiRuntimeDecreasing); err != nil {
			return fmt.Errorf("failed to update container cgroup RT: %w", err)
		}
	}
	return nil
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

func writeRtValues(cgPath string, period int, runtime int, core *string, periodDecreasing, runtimeDecreasing, multiRuntimeDecreasing bool) error {
	writeRuntime := runtime
	if writeRuntime < 0 {
		writeRuntime = 0
	}

	cpuRTPeriodPath := filepath.Join(cgPath, cgroupRTPeriod)

	if periodDecreasing || runtimeDecreasing || multiRuntimeDecreasing {
		// When decreasing, set runtime first, then period
		if err := writeRuntimeFile(cgPath, writeRuntime, core); err != nil {
			return err
		}

		// Check if period needs to be updated
		currentPeriod, err := readIntFromFile(cpuRTPeriodPath)
		if err == nil && currentPeriod == period {
			log.Printf("Period already matches for %s: %d, skipping write", cgPath, period)
		} else {
			if err := writeCgroupFile(cpuRTPeriodPath, fmt.Sprintf("%d", period)); err != nil {
				return err
			}
		}
	} else {
		// When increasing, set period first, then runtime
		// Check if period needs to be updated
		currentPeriod, err := readIntFromFile(cpuRTPeriodPath)
		if err == nil && currentPeriod == period {
			log.Printf("Period already matches for %s: %d, skipping write", cgPath, period)
		} else {
			if err := writeCgroupFile(cpuRTPeriodPath, fmt.Sprintf("%d", period)); err != nil {
				return err
			}
		}

		if err := writeRuntimeFile(cgPath, writeRuntime, core); err != nil {
			return err
		}
	}

	// Apply CPU set if core is specified
	if core != nil {
		cpuSetPath := filepath.Join(cgPath, cgroupCPUSet)
		
		// Read current cpuset value to check if change is needed
		currentCpuSet, err := os.ReadFile(cpuSetPath)
		if err != nil {
			return fmt.Errorf("failed to read current cpuset: %w", err)
		}
		
		currentValue := strings.TrimSpace(string(currentCpuSet))
		if currentValue != *core {
			log.Printf("Updating CPU set for %s: %s -> %s", cgPath, currentValue, *core)
			
			if err := writeCgroupFile(cpuSetPath, *core); err != nil {
				return err
			}
		} else {
			log.Printf("CPU set already matches for %s: %s", cgPath, *core)
		}
	}

	return nil
}

// writeRuntimeFile writes the runtime value to the appropriate cgroup file
// Handles both multi-runtime (core-specific) and global runtime
func writeRuntimeFile(cgPath string, runtime int, core *string) error {
	if core != nil {
		// Use multi-runtime format for core-specific runtime
		cpuRTMultiRuntimePath := filepath.Join(cgPath, cgroupRTMultiRuntime)
		multiRuntimeValue := fmt.Sprintf("%s %d", *core, runtime)
		return writeCgroupFile(cpuRTMultiRuntimePath, multiRuntimeValue)
	}
	
	// Use standard runtime for global runtime
	cpuRTRuntimePath := filepath.Join(cgPath, cgroupRTRuntime)
	return writeCgroupFile(cpuRTRuntimePath, fmt.Sprintf("%d", runtime))
}

// getCurrentMultiRuntime reads the current multi-runtime value for a specific core range
func getCurrentMultiRuntime(cgPath string, coreRange string) int {
	cpuRTMultiRuntimePath := filepath.Join(cgPath, cgroupRTMultiRuntime)
	data, err := os.ReadFile(cpuRTMultiRuntimePath)
	if err != nil {
		return 0
	}
	
	content := string(data)
	if !strings.Contains(content, coreRange) {
		return 0
	}
	
	fields := strings.Fields(content)
	for i := 0; i < len(fields); i += 2 {
		if i+1 < len(fields) && fields[i] == coreRange {
			if rt, err := strconv.Atoi(fields[i+1]); err == nil {
				return rt
			}
		}
	}
	return 0
}

// applyRuntimeWithOrder applies runtime-only updates in the correct order based on increase/decrease
func applyRuntimeWithOrder(containerPath, podPath string, runtime int, core *string, decreasing bool) error {
	if decreasing {
		// Decreasing: Container (child) first, then Pod (parent)
		log.Printf("Runtime-only mode: decreasing runtime, container first")
		if err := writeRuntimeOnly(containerPath, runtime, core); err != nil {
			return fmt.Errorf("failed to update container runtime: %w", err)
		}
		if err := writeRuntimeOnly(podPath, runtime, core); err != nil {
			return fmt.Errorf("failed to update pod runtime: %w", err)
		}
	} else {
		// Increasing: Pod (parent) first, then Container (child)
		log.Printf("Runtime-only mode: increasing runtime, pod first")
		if err := writeRuntimeOnly(podPath, runtime, core); err != nil {
			return fmt.Errorf("failed to update pod runtime: %w", err)
		}
		if err := writeRuntimeOnly(containerPath, runtime, core); err != nil {
			return fmt.Errorf("failed to update container runtime: %w", err)
		}
	}
	return nil
}

// readRtValues reads current cpu.rt_period_us and cpu.rt_runtime_us under the given cgroup path
func readRtValues(cgPath string) (int, int, error) {
	periodPath := filepath.Join(cgPath, cgroupRTPeriod)
	runtimePath := filepath.Join(cgPath, cgroupRTRuntime)
	curPeriod, err := readIntFromFile(periodPath)
	if err != nil {
		return 0, 0, fmt.Errorf("read %s: %w", periodPath, err)
	}
	curRuntime, err := readIntFromFile(runtimePath)
	if err != nil {
		return 0, 0, fmt.Errorf("read %s: %w", runtimePath, err)
	}
	return curPeriod, curRuntime, nil
}

// writeRuntimeOnly updates only the runtime value without touching the period
// Used for escalation mode to avoid "device or resource busy" errors
func writeRuntimeOnly(cgPath string, runtime int, core *string) error {
	log.Printf("Writing runtime only for %s: runtime=%d, core=%v", cgPath, runtime, core)
	return writeRuntimeFile(cgPath, runtime, core)
}

// writeCgroupFile writes to a cgroup file without truncating (O_WRONLY)
// This prevents "device or resource busy" errors when RT tasks are running
func writeCgroupFile(path string, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()
	
	if _, err := f.WriteString(value); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// isDecrease returns true if newVal is strictly less than oldVal
func isDecrease(oldVal int, newVal int) bool {
	return newVal < oldVal
}

// isRuntimeDecrease handles special semantics where negative means unlimited (loosest).
// Decrease cases:
// - old < 0 and new >= 0 (from unlimited to limited)
// - both non-negative and new < old
// Increase cases:
// - new < 0 (to unlimited)
// - both non-negative and new >= old
func isRuntimeDecrease(oldRuntime int, newRuntime int) bool {
	if newRuntime < 0 {
		return false
	}
	if oldRuntime < 0 && newRuntime >= 0 {
		return true
	}
	return newRuntime < oldRuntime
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
	sliceGroup := parts[0]  // e.g. kubepods-besteffort-podXXXX.slice
	runtimeName := parts[1] // e.g. cri-containerd
	scopeID := parts[2]     // e.g. <container-id>
	// Derive QoS slice (segment before -pod) and pod slice
	idx := strings.LastIndex(sliceGroup, "-pod")
	if idx <= 0 {
		return "", "", fmt.Errorf("cannot locate pod segment in slice: %s", sliceGroup)
	}
	qosSlice := sliceGroup[:idx] + ".slice" // e.g. kubepods-besteffort.slice
	podSlice := sliceGroup                  // full pod slice name (already ends with .slice)
	// Build absolute pod path under unified hierarchy
	podPath := filepath.Join(cgroupV2Root, "kubepods.slice", qosSlice, podSlice)
	containerScope := runtimeName + "-" + scopeID + ".scope"
	containerPath := filepath.Join(podPath, containerScope)
	return containerPath, podPath, nil
}
