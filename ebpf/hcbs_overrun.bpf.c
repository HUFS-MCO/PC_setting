// hcbs_overrun.bpf.c
// Build:
//   clang -O2 -g -target bpf -D__TARGET_ARCH_x86 -c hcbs_overrun.bpf.c -o hcbs_overrun.bpf.o
//   bpftool gen skeleton hcbs_overrun.bpf.o > hcbs_overrun.skel.h

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_tracing.h>

char LICENSE[] SEC("license") = "Dual BSD/GPL";

// ========== 이벤트 구조체 ==========
struct event {
    __u64 ts;          // timestamp (ns)
    __u32 cpu;
    __u32 pad;
    __s64 runtime_ns;  // dl_se->runtime
    __u32 _pad;
    __u64 tg_ptr;
    __u64 cgid;
};

// 전이 상태 추적
struct last_state {
    __u8 was_pos;
    __u8 _pad[7];
};

// ========== MAP 정의 ==========
SEC(".maps")
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 1 << 24);  // 16MB (원래 1MB에서 증가)
} events SEC(".maps");

SEC(".maps")
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 8192);
    __type(key, __u64);
    __type(value, struct last_state);
} se_state SEC(".maps");


// ========== Main Trace Hook ==========
SEC("fentry/update_curr_dl_se")
int BPF_PROG(on_update_curr_dl_se, struct rq *rq, struct sched_dl_entity *dl_se, s64 delta_exec)
{
    if (!dl_se)
        return 0;

    // 1️⃣ dl_server가 아닌 태스크 제외
    __u8 dl_server = BPF_CORE_READ_BITFIELD_PROBED(dl_se, dl_server);
    if (!dl_server)
        return 0;

    // 2️⃣ cgroup task_group 포인터 획득
    struct rq *my_q = BPF_CORE_READ(dl_se, my_q);
    struct task_group *tg = NULL;
    if (my_q)
        tg = BPF_CORE_READ(my_q, rt.tg);
    if (!tg)
        return 0;

    // 4️⃣ 현재 런타임 읽기
    s64 runtime = BPF_CORE_READ(dl_se, runtime);
    __u8 throttled = BPF_CORE_READ_BITFIELD_PROBED(dl_se, dl_throttled);

    // 5️⃣ 상태 추적용 키/값
    __u64 key = (__u64)dl_se;
    struct last_state init = { .was_pos = 1 };
    struct last_state *st = bpf_map_lookup_elem(&se_state, &key);
    if (!st) {
        bpf_map_update_elem(&se_state, &key, &init, BPF_ANY);
        st = bpf_map_lookup_elem(&se_state, &key);
        if (!st)
            return 0;
    }

    // 6️⃣ 첫 소진 이벤트만 전송 
    if (st->was_pos && runtime <= 0 && throttled && BPF_CORE_READ(dl_se, dl_runtime) != 50000000) {
        struct event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
        if (e) {
            e->ts = bpf_ktime_get_ns();
            e->cpu = bpf_get_smp_processor_id();
            e->runtime_ns = runtime; //BPF_CORE_READ(dl_se, dl_runtime);
            e->tg_ptr = (unsigned long)tg;
            e->cgid = bpf_get_current_cgroup_id();
            bpf_ringbuf_submit(e, 0);
        }
        st->was_pos = 0;
    } else if (!st->was_pos && runtime > 0) {
        // runtime이 다시 보충되면 복귀
        st->was_pos = 1;
    }

    return 0;
}
