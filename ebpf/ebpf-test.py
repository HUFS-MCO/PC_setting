#!/usr/bin/python3
from bcc import BPF
import ctypes as ct
from datetime import datetime

bpf_text = """
#include <linux/sched.h>

struct data_t {
    u64 cgroup_id;
    u64 timestamp;
    u32 pid;
    char comm[TASK_COMM_LEN];
};

BPF_PERF_OUTPUT(events);

// update_curr_dl_se의 반환값이나 내부 로직을 추적
// kretprobe로 함수 반환 시점에 확인
int trace_update_return(struct pt_regs *ctx) {
    struct task_struct *p = (struct task_struct *)bpf_get_current_task();
    
    if (p->policy != SCHED_FIFO) {
        return 0;
    }
    
    s64 runtime = 0;
    bpf_probe_read_kernel(&runtime, sizeof(runtime), &p->dl.runtime);
    
    // runtime이 음수이면 exceeded 발생
    if (runtime <= 0) {
        struct data_t data = {};
        
        data.cgroup_id = bpf_get_current_cgroup_id();
        data.timestamp = bpf_ktime_get_ns();
        u64 pid_tgid = bpf_get_current_pid_tgid();
        data.pid = pid_tgid >> 32;
        bpf_get_current_comm(&data.comm, sizeof(data.comm));
        
        events.perf_submit(ctx, &data, sizeof(data));
    }
    
    return 0;
}
"""

class Data(ct.Structure):
    _fields_ = [
        ("cgroup_id", ct.c_ulonglong),
        ("timestamp", ct.c_ulonglong),
        ("pid", ct.c_uint),
        ("comm", ct.c_char * 16)
    ]

def print_event(cpu, data, size):
    event = ct.cast(data, ct.POINTER(Data)).contents
    dt = datetime.now()
    print(f"[{dt.strftime('%H:%M:%S.%f')[:-3]}] "
          f"🔴 Runtime EXCEEDED! "
          f"PID: {event.pid}, "
          f"Comm: {event.comm.decode('utf-8', 'replace')}, "
          f"Cgroup: {event.cgroup_id}")

if __name__ == "__main__":
    try:
        b = BPF(text=bpf_text)
        
        # kprobe와 kretprobe 모두 사용
        b.attach_kprobe(event="update_curr_dl_se", fn_name="trace_update_return")
        b.attach_kretprobe(event="update_curr_dl_se", fn_name="trace_update_return")
        
        print("✅ Monitoring update_curr_dl_se (entry and return)")
        print("   Press Ctrl+C to exit.\n")
        
        b["events"].open_perf_buffer(print_event)
        
        while True:
            b.perf_buffer_poll()
            
    except KeyboardInterrupt:
        print("\n🛑 Stopped.")
    except Exception as e:
        print(f"❌ Error: {e}")
        import traceback
        traceback.print_exc()
