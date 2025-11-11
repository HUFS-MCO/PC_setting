package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

const (
	annUsageKey   = "mckube.sdv.com/cpu-usage"
	annDurKey     = "mckube.sdv.com/cpu-over90-duration-s"
	annCpuBusyKey = "mckube.sdv.com/isCpuBusy"
	
	// API Server 통신 설정
	apiServerPort = "8888"
	apiEndpoint   = "/api/v1/nodes/%s/annotations"
)

// ResponseMessage 응답용 구조체
type ResponseMessage struct {
	Message string `json:"message"`
	Success bool   `json:"success"`
}

type cpuSample struct{ idle, total uint64 }

func readProcStat() (cpuSample, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return cpuSample{}, errors.New("empty /proc/stat")
	}
	fields := strings.Fields(sc.Text())
	if len(fields) == 0 || fields[0] != "cpu" {
		return cpuSample{}, errors.New("unexpected /proc/stat format")
	}
	var nums []uint64
	for _, s := range fields[1:] {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return cpuSample{}, err
		}
		nums = append(nums, v)
	}
	var user, nice, system, idle, iowait, irq, softirq, steal uint64
	if len(nums) > 0 {
		user = nums[0]
	}
	if len(nums) > 1 {
		nice = nums[1]
	}
	if len(nums) > 2 {
		system = nums[2]
	}
	if len(nums) > 3 {
		idle = nums[3]
	}
	if len(nums) > 4 {
		iowait = nums[4]
	}
	if len(nums) > 5 {
		irq = nums[5]
	}
	if len(nums) > 6 {
		softirq = nums[6]
	}
	if len(nums) > 7 {
		steal = nums[7]
	}
	idleAll := idle + iowait
	nonIdle := user + nice + system + irq + softirq + steal
	return cpuSample{idle: idleAll, total: idleAll + nonIdle}, nil
}

func computeUsage(prev, cur cpuSample) int {
	if cur.total <= prev.total {
		return 0
	}
	idleDelta := float64(cur.idle - prev.idle)
	totalDelta := float64(cur.total - prev.total)
	usage := (1.0 - idleDelta/totalDelta) * 100.0
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return int(math.Round(usage))
}

func getNodeName() string {
	if v := strings.TrimSpace(os.Getenv("NODE_NAME")); v != "" {
		return strings.ToLower(v)
	}
	if hostname, err := os.Hostname(); err == nil {
		return strings.ToLower(strings.TrimSpace(hostname))
	}
	return "unknown"
}

// NodeAnnotationRequest HTTP 요청용 구조체
type NodeAnnotationRequest struct {
	Annotations map[string]string `json:"annotations"`
}

// updateNodeAnnotations kubectl을 통해 실제 node annotation 업데이트
func updateNodeAnnotations(nodeName string, annotations map[string]string) error {
	kubectl := "/usr/bin/kubectl"
	if envKubectl := strings.TrimSpace(os.Getenv("KUBECTL")); envKubectl != "" {
		kubectl = envKubectl
	}

	// kubectl annotate 명령어 구성
	args := []string{"annotate", "node", nodeName, "--overwrite"}
	
	for key, value := range annotations {
		args = append(args, fmt.Sprintf("%s=%s", key, value))
	}

	cmd := exec.Command(kubectl, args...)
	cmd.Env = os.Environ() // KUBECONFIG 등 환경변수 전달
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl annotate failed: %v, output: %s", err, string(output))
	}
	
	log.Printf("Successfully updated annotations for node %s: %v", nodeName, annotations)
	return nil
}

// handleNodeAnnotations node annotation 업데이트 핸들러
func handleNodeAnnotations(w http.ResponseWriter, r *http.Request) {
	// URL에서 노드 이름 추출
	vars := mux.Vars(r)
	nodeName := vars["nodeName"]
	
	if nodeName == "" {
		http.Error(w, "Node name is required", http.StatusBadRequest)
		return
	}

	// POST 또는 PATCH 메서드만 허용
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 요청 본문 파싱
	var req NodeAnnotationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Annotation 업데이트
	if err := updateNodeAnnotations(nodeName, req.Annotations); err != nil {
		log.Printf("Failed to update annotations for node %s: %v", nodeName, err)
		response := ResponseMessage{
			Message: fmt.Sprintf("Failed to update annotations: %v", err),
			Success: false,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(response)
		return
	}

	// 성공 응답
	response := ResponseMessage{
		Message: fmt.Sprintf("Annotations updated successfully for node %s", nodeName),
		Success: true,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// healthHandler 헬스체크 핸들러
func healthHandler(w http.ResponseWriter, r *http.Request) {
	response := ResponseMessage{
		Message: "API server is healthy",
		Success: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// annotateViaHTTP 로컬 HTTP API를 통한 node annotation 업데이트
func annotateViaHTTP(node string, usage int, over90time int64, isCpuBusy string) error {
	url := fmt.Sprintf("http://localhost:%s/api/v1/nodes/%s/annotations", apiServerPort, node)
	
	// 요청 데이터 생성
	reqData := NodeAnnotationRequest{
		Annotations: map[string]string{
			annUsageKey:   fmt.Sprintf("%d", usage),
			annDurKey:     fmt.Sprintf("%d", over90time),
			annCpuBusyKey: isCpuBusy,
		},
	}
	
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return fmt.Errorf("failed to marshal request data: %v", err)
	}
	
	// HTTP 요청 생성
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "PATCH", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %v", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	
	// HTTP 클라이언트로 요청 전송
	client := &http.Client{
		Timeout: 3 * time.Second,
	}
	
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send HTTP request: %v", err)
	}
	defer resp.Body.Close()
	
	// 응답 확인
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	log.Printf("annotate success via HTTP: node=%s usage=%d%% over90time=%ds isCpuBusy=%s", 
		node, usage, over90time, isCpuBusy)
	return nil
}

// startAPIServer HTTP API 서버 시작
func startAPIServer() {
	log.Printf("Starting HTTP API Server on port %s", apiServerPort)

	// 라우터 설정
	r := mux.NewRouter()
	
	// API 엔드포인트
	r.HandleFunc("/api/v1/nodes/{nodeName}/annotations", handleNodeAnnotations).Methods("POST", "PATCH")
	r.HandleFunc("/health", healthHandler).Methods("GET")
	
	// 루트 경로
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "MC-Kube Node Annotation API Server - Port %s\n", apiServerPort)
	})

	// HTTP 서버 설정
	server := &http.Server{
		Addr:         ":" + apiServerPort,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("API server listening on :%s", apiServerPort)
	log.Printf("Endpoints:")
	log.Printf("  POST/PATCH /api/v1/nodes/{nodeName}/annotations - Update node annotations")
	log.Printf("  GET /health - Health check")
	
	if err := server.ListenAndServe(); err != nil {
		log.Printf("API server failed: %v", err)
	}
}

// annotateViaHTTP HTTP API를 통한 node annotation 업데이트
func annotateViaHTTP(node string, usage int, over90time int64, isCpuBusy string) error {
	// API Server 주소 설정 (마스터 노드 또는 로드밸런서 주소)
	apiServerHost := strings.TrimSpace(os.Getenv("API_SERVER_HOST"))
	if apiServerHost == "" {
		apiServerHost = "localhost" // 기본값
	}
	
	url := fmt.Sprintf("http://%s:%s%s", apiServerHost, apiServerPort, fmt.Sprintf(apiEndpoint, node))
	
	// 요청 데이터 생성
	reqData := NodeAnnotationRequest{
		Annotations: map[string]string{
			annUsageKey:   fmt.Sprintf("%d", usage),
			annDurKey:     fmt.Sprintf("%d", over90time),
			annCpuBusyKey: isCpuBusy,
		},
	}
	
	jsonData, err := json.Marshal(reqData)
	if err != nil {
		return fmt.Errorf("failed to marshal request data: %v", err)
	}
	
	// HTTP 요청 생성
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "PATCH", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %v", err)
	}
	
	req.Header.Set("Content-Type", "application/json")
	
	// 인증 토큰 추가 (ServiceAccount 토큰)
	if token := strings.TrimSpace(os.Getenv("SERVICE_ACCOUNT_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	
	// HTTP 클라이언트로 요청 전송
	client := &http.Client{
		Timeout: 3 * time.Second,
	}
	
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send HTTP request: %v", err)
	}
	defer resp.Body.Close()
	
	// 응답 확인
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	log.Printf("annotate success via HTTP: node=%s usage=%d%% over90time=%ds isCpuBusy=%s", 
		node, usage, over90time, isCpuBusy)
	return nil
}

// annotate kubectl 명령어 기반 annotation 업데이트 (fallback)
func annotateViaKubectl(node string, usage int, over90time int64, isCpuBusy string) error {
	// SERVICE_ACCOUNT_TOKEN 환경변수가 있으면 HTTP API 우선 사용
	if strings.TrimSpace(os.Getenv("SERVICE_ACCOUNT_TOKEN")) != "" {
		if err := annotateViaHTTP(node, usage, over90time, isCpuBusy); err == nil {
			return nil
		}
		log.Printf("HTTP API failed, falling back to kubectl: %v", err)
	}
	
	// kubectl 명령어 실행 (기존 방식)
	return annotateViaKubectlDirect(node, usage, over90time, isCpuBusy)
}

func annotateViaKubectlDirect(node string, usage int, over90time int64, isCpuBusy string) error {
	// kubectl은 더 이상 import하지 않으므로 외부 명령어로 실행
	log.Printf("kubectl method not available in HTTP-only mode")
	return fmt.Errorf("kubectl method not available")
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// HTTP API 서버를 고루틴으로 시작
	go startAPIServer()

	// 잠시 대기하여 API 서버가 시작되도록 함
	time.Sleep(2 * time.Second)

	interval := time.Second // 🔒 1초 고정
	node := getNodeName()
	if node == "" {
		log.Fatal("Cannot determine node name")
	}
	log.Printf("mckube-cpu-agent starting: node=%s interval=%s", node, interval)

	prev, err := readProcStat()
	if err != nil {
		log.Fatalf("read /proc/stat: %v", err)
	}
	time.Sleep(interval)

	var over90time int64
	var lastAnnUsage = -1
	var lastAnnTime time.Time
	var droppedBelowTime time.Time // CPU가 90% 미만으로 떨어진 시점
	var waitingForBusyFalse bool   // isCpuBusy를 false로 보내기 위해 대기 중인지 여부

	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		cur, err := readProcStat()
		if err != nil {
			log.Printf("read /proc/stat error: %v", err)
			continue
		}
		u := computeUsage(prev, cur)
		prev = cur

		if u > 90 {
			over90time += int64(interval / time.Second)
		} else {
			over90time = 0
		}

		log.Printf("publishing cpu usage: node=%s usage=%d%% over90time=%ds", node, u, over90time)

		// CPU 사용량이 90% 이상일 때 annotation 갱신 (이벤트 기반 트리거)
		if u > 90 {
			// 90% 이상이면 대기 상태 해제
			waitingForBusyFalse = false

			if (u != lastAnnUsage) || time.Since(lastAnnTime) > 5*time.Second {
				if err := annotateViaHTTP(node, u, over90time, "true"); err == nil {
					lastAnnUsage = u
					lastAnnTime = time.Now()
				}
			}
		} else {
			// CPU가 90% 미만으로 떨어진 경우
			if lastAnnUsage > 90 {
				// 최초로 90% 미만으로 떨어진 시점
				log.Printf("CPU dropped below 90%%, sending reset annotation: node=%s usage=%d%%", node, u)
				if err := annotateViaHTTP(node, u, over90time, "true"); err == nil {
					lastAnnUsage = u
					lastAnnTime = time.Now()
					droppedBelowTime = time.Now()
					waitingForBusyFalse = true
				}
			} else if waitingForBusyFalse && time.Since(droppedBelowTime) >= 5*time.Second {
				// 5초 후에도 90% 미만이면 isCpuBusy를 false로 설정
				log.Printf("5 seconds passed below 90%%, setting isCpuBusy to false: node=%s usage=%d%%", node, u)
				if err := annotateViaHTTP(node, u, over90time, "false"); err == nil {
					lastAnnUsage = u
					lastAnnTime = time.Now()
					waitingForBusyFalse = false
				}
			}
		}
	}
}
