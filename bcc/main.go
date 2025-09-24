package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"os"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

type cgCtl struct {
	PeriodNS  uint64
	RuntimeNS uint64
}

const (
	cntCycleOK   = 0
	cntCycleFail = 1
)

func sumPerCPUArray(m *ebpf.Map, idx uint32) (uint64, error) {
	var vals []uint64
	if err := m.Lookup(idx, &vals); err != nil {
		return 0, err
	}
	var sum uint64
	for _, v := range vals {
		sum += v
	}
	return sum, nil
}

func main() {
	var (
		pid       = flag.Int("pid", 0, "컨테이너 내부 대표 PID(TGID). 이 PID의 cgroup에 파라미터 적용.")
		periodUS  = flag.Uint64("period-us", 12000, "컨테이너 주기 (마이크로초).")
		runtimeUS = flag.Uint64("runtime-us", 3000,  "컨테이너 런타임 (마이크로초).")
		printInt  = flag.Duration("print-every", time.Second, "통계 출력 주기(0이면 미출력).")
	)
	flag.Parse()

	if *pid <= 0 {
		log.Fatalf("-pid 필요")
	}
	if *runtimeUS == 0 || *periodUS == 0 || *runtimeUS > *periodUS {
		log.Fatalf("잘못된 파라미터: 0 < runtime <= period")
	}

	if err := rlimit.RemoveMemlock(); err != nil {
		log.Fatalf("RemoveMemlock: %v", err)
	}

	var objs ebpfObjects
	// CO-RE 리로케이션을 쓰지 않으므로 CollectionOptions 불필요
	if err := loadEbpfObjects(&objs, nil); err != nil {
		log.Fatalf("load eBPF objects: %v", err)
	}
	defer objs.Close()

	// attach: sched_switch tracepoint
	swL, err := link.Tracepoint("sched", "sched_switch", objs.TpSchedSwitch, nil)
	if err != nil {
		log.Fatalf("attach tracepoint sched_switch: %v", err)
	}
	defer swL.Close()

	// attach: signal_deliver tracepoint
	sigL, err := link.Tracepoint("signal", "signal_deliver", objs.TpSignalDeliver, nil)
	if err != nil {
		log.Fatalf("attach tracepoint signal_deliver: %v", err)
	}
	defer sigL.Close()

	// attach: kprobe(do_exit)
	// exitL, err := link.Kprobe("do_exit", objs.KpDoExit, nil)
	// if err != nil {
	// 	log.Fatalf("attach kprobe do_exit: %v", err)
	// }
	// defer exitL.Close()

	// pid 기반으로 제어 파라미터 주입 (eBPF가 최초 스케줄에서 cgroup으로 승격)
	pctl := cgCtl{
		PeriodNS:  *periodUS * 1000,
		RuntimeNS: *runtimeUS * 1000,
	}
	if err := objs.PidCtlMap.Update(uint32(*pid), pctl, ebpf.UpdateAny); err != nil {
		log.Fatalf("pid_ctl_map update: %v", err)
	}
	fmt.Printf("[INFO] Set control via pid=%d: period=%dus runtime=%dus\n",
		*pid, *periodUS, *runtimeUS)

	// 출력 루프
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *printInt > 0 {
		t := time.NewTicker(*printInt)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				goto done
			case <-t.C:
				ok, _ := sumPerCPUArray(objs.Counters, cntCycleOK)
				fail, _ := sumPerCPUArray(objs.Counters, cntCycleFail)
				fmt.Printf("[STAT] cycles: ok=%d, fail=%d\n", ok, fail)
			}
		}
	}

done:
	ok, _ := sumPerCPUArray(objs.Counters, cntCycleOK)
	fail, _ := sumPerCPUArray(objs.Counters, cntCycleFail)
	fmt.Printf("[RESULT] cycles: ok=%d, fail=%d\n", ok, fail)
}
