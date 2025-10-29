package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type cpuSample struct{ idle, total uint64 }

var k8sClient kubernetes.Interface

// Annotation 키 (controller와 일치해야 함)
const (
	annUsageKey   = "mckube.sdv.com/cpu-usage"
	annDurKey     = "mckube.sdv.com/cpu-over90-duration-s"
	annCpuBusyKey = "mckube.sdv.com/isCpuBusy"
)

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

func initKubernetesClient() error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to get in-cluster config: %v", err)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	k8sClient = client
	return nil
}

func updateNodeAnnotations(nodeName string, cpuUsage int, over90Duration int64, isCpuBusy bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	patchData := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{
				annUsageKey:   strconv.Itoa(cpuUsage),
				annDurKey:     strconv.FormatInt(over90Duration, 10),
				annCpuBusyKey: strconv.FormatBool(isCpuBusy),
			},
		},
	}

	patchBytes, err := json.Marshal(patchData)
	if err != nil {
		return fmt.Errorf("failed to marshal patch data: %v", err)
	}

	_, err = k8sClient.CoreV1().Nodes().Patch(
		ctx,
		nodeName,
		types.MergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)

	if err != nil {
		return fmt.Errorf("failed to patch node annotations: %v", err)
	}

	return nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	interval := time.Second
	node := getNodeName()

	if node == "" {
		log.Fatal("Cannot determine node name")
	}

	if err := initKubernetesClient(); err != nil {
		log.Fatalf("Failed to initialize Kubernetes client: %v", err)
	}

	log.Printf("mckube-cpu-agent starting: node=%s interval=%s", node, interval)

	prev, err := readProcStat()
	if err != nil {
		log.Fatalf("read /proc/stat: %v", err)
	}
	time.Sleep(interval)

	var over90time int64
	var lastSentUsage = -1
	var isCpuBusy = false

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

		cpuWasBusy := isCpuBusy
		isCpuBusy = u > 90

		if isCpuBusy {
			over90time += int64(interval / time.Second)

			if !cpuWasBusy {
				log.Printf("CPU usage exceeded 90%%: node=%s usage=%d%% over90time=%ds", node, u, over90time)
			} else if u != lastSentUsage && over90time%5 == 0 {
				log.Printf("CPU high usage continues: node=%s usage=%d%% over90time=%ds", node, u, over90time)
			}
		} else {
			if cpuWasBusy {
				log.Printf("CPU usage normalized: node=%s usage=%d%%", node, u)
			}
			over90time = 0
		}

		shouldSend := false

		if isCpuBusy && !cpuWasBusy {
			// CPU가 90% 이상으로 올라간 경우
			shouldSend = true
		} else if !isCpuBusy && cpuWasBusy {
			log.Printf("CPU dropped below 90%%: node=%s usage=%d%%", node, u)
			shouldSend = true
		} else if isCpuBusy && over90time > 0 && over90time%5 == 0 {
			// CPU가 계속 90% 이상이면서 5초마다 한 번씩만 업데이트
			shouldSend = true
		}

		if shouldSend {
			if err := updateNodeAnnotations(node, u, over90time, isCpuBusy); err != nil {
				log.Printf("Failed to update node annotations: %v", err)
			} else {
				lastSentUsage = u
				if isCpuBusy {
					log.Printf("Node annotations updated (HIGH CPU): node=%s usage=%d%% over90time=%ds",
						node, u, over90time)
				} else {
					log.Printf("Node annotations updated (CPU normalized): node=%s usage=%d%% over90time=%ds",
						node, u, over90time)
				}
			}
		}
	}
}
