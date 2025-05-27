package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
)

type ReniceRequest struct {
	ContainerID string `json:"container_id"`
	Nice        int    `json:"nice"`
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

	listener, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		log.Fatalf("Failed to listen on socket: %v", err)
	}
	defer listener.Close()

	log.Println("Renicer daemon listening on", socketPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/renice", handleRenice)

	if err := http.Serve(listener, mux); err != nil {
		log.Fatalf("HTTP server error: %v", err)
	}
}
