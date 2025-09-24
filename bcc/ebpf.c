//go:build ignore
// +build ignore

// ebpf.c — BTF 없는 모드: tracepoint/sched/sched_switch + kprobe/do_exit
#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

char LICENSE[] SEC("license") = "GPL";

struct cg_ctl {
    __u64 period_ns;
    __u64 runtime_ns;
};

struct cg_state {
    __u64 cycle_start_ns;
    __u64 consumed_ns;
    __u8  in_runtime;
    __u8  saw_sigusr1;
    __u8  _pad[6];
};

/* cgroup_id -> DL control(period/runtime) */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 2048);
    __type(key, __u64);
    __type(value, struct cg_ctl);
} cg_ctl_map SEC(".maps");

/* pid (tgid) -> DL control (최초 관측 시 cgroup으로 승격) */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 4096);
    __type(key, __u32);
    __type(value, struct cg_ctl);
} pid_ctl_map SEC(".maps");

/* cgroup_id -> 상태 */
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 8192);
    __type(key, __u64);
    __type(value, struct cg_state);
} cg_state_map SEC(".maps");

/* 전역 per-CPU 카운터 */
enum { CNT_CYCLE_OK = 0, CNT_CYCLE_FAIL = 1, CNT_MAX = 2 };
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, CNT_MAX);
    __type(key, __u32);
    __type(value, __u64);
} counters SEC(".maps");

static __always_inline void cnt_inc(__u32 idx)
{
    __u64 *v = bpf_map_lookup_elem(&counters, &idx);
    if (v) (*v)++;
}

/* per-CPU로 마지막 스케줄된 cgroup과 타임스탬프 보관 */
struct cpu_state {
    __u64 last_ts;
    __u64 last_cgid;
};
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, struct cpu_state);
} cpu_last SEC(".maps");

/* pid -> cg_ctl 승격 */
static __always_inline void maybe_promote_pid_to_cg(__u32 tgid, __u64 cgid)
{
    struct cg_ctl *pidctl = bpf_map_lookup_elem(&pid_ctl_map, &tgid);
    if (!pidctl) return;

    struct cg_ctl *cgctl = bpf_map_lookup_elem(&cg_ctl_map, &cgid);
    if (!cgctl) {
        bpf_map_update_elem(&cg_ctl_map, &cgid, pidctl, BPF_ANY);
    }
}

/* 주기 롤오버 및 주기 시작 */
static __always_inline void maybe_roll_period(struct cg_state *st, const struct cg_ctl *ctl, __u64 now)
{
    if (!ctl || !ctl->period_ns) return;

    if (!st->cycle_start_ns) {
        st->cycle_start_ns = now;
        st->consumed_ns    = 0;
        st->in_runtime     = 1;
        st->saw_sigusr1    = 0;
        return;
    }

    if (now - st->cycle_start_ns >= ctl->period_ns) {
        if (st->in_runtime) {
            if (st->saw_sigusr1) cnt_inc(CNT_CYCLE_OK);
            else                 cnt_inc(CNT_CYCLE_FAIL);
            st->in_runtime = 0;
        }
        st->cycle_start_ns = now;
        st->consumed_ns    = 0;
        st->in_runtime     = 1;
        st->saw_sigusr1    = 0;
    }
}

/* 스케줄 전환: per-CPU last_*로 직전 cgroup에 on-CPU 시간을 누적 */
SEC("tracepoint/sched/sched_switch")
int tp_sched_switch(void *ctx)
{
    __u64 now = bpf_ktime_get_ns();

    /* 현재 스케줄된 태스크의 cgroup (스위치 후 'next'가 current) */
    __u64 cur_cgid = bpf_get_current_cgroup_id();

    /* per-CPU 상태 가져오기 */
    __u32 key0 = 0;
    struct cpu_state *cpu = bpf_map_lookup_elem(&cpu_last, &key0);
    if (!cpu) return 0;

    /* 직전 cgroup에 delta 누적 */
    if (cpu->last_ts && now > cpu->last_ts && cpu->last_cgid) {
        __u64 delta = now - cpu->last_ts;

        struct cg_state *pst = bpf_map_lookup_elem(&cg_state_map, &cpu->last_cgid);
        struct cg_ctl   *pctl = bpf_map_lookup_elem(&cg_ctl_map, &cpu->last_cgid);
        if (pst && pctl) {
            pst->consumed_ns += delta;
            if (pst->in_runtime && pctl->runtime_ns && pst->consumed_ns >= pctl->runtime_ns) {
                if (pst->saw_sigusr1) cnt_inc(CNT_CYCLE_OK);
                else                  cnt_inc(CNT_CYCLE_FAIL);
                pst->in_runtime = 0;
            }
        }
    }

    /* 현재 cgroup의 주기 롤오버/시작 */
    if (cur_cgid) {
        /* pid 기반 승격 (현재 TGID 사용) */
        __u64 id = bpf_get_current_pid_tgid();
        __u32 tgid = id >> 32;
        maybe_promote_pid_to_cg(tgid, cur_cgid);

        struct cg_state *cst = bpf_map_lookup_elem(&cg_state_map, &cur_cgid);
        struct cg_ctl   *cctl = bpf_map_lookup_elem(&cg_ctl_map,  &cur_cgid);
        if (!cst && cctl) {
            struct cg_state init = {};
            bpf_map_update_elem(&cg_state_map, &cur_cgid, &init, BPF_ANY);
            cst = bpf_map_lookup_elem(&cg_state_map, &cur_cgid);
        }
        if (cst && cctl) {
            maybe_roll_period(cst, cctl, now);
        }
    }

    /* per-CPU 상태 갱신: 현재를 last로 */
    cpu->last_ts   = now;
    cpu->last_cgid = cur_cgid;

    return 0;
}

/* 시그널 전달 시점: 런타임 창 안에서만 플래그 세팅 */
SEC("tracepoint/signal/signal_deliver")
int tp_signal_deliver(struct trace_event_raw_signal_deliver *ctx)
{
    if (ctx->sig != 10 /* SIGUSR1 */)
        return 0;

    __u64 cg_id = bpf_get_current_cgroup_id();
    if (!cg_id) return 0;

    /* pid 기반 승격 */
    __u64 id = bpf_get_current_pid_tgid();
    __u32 tgid = id >> 32;
    maybe_promote_pid_to_cg(tgid, cg_id);

    struct cg_state *st = bpf_map_lookup_elem(&cg_state_map, &cg_id);
    struct cg_ctl   *ctl = bpf_map_lookup_elem(&cg_ctl_map, &cg_id);
    if (!st || !ctl) return 0;

    if (st->in_runtime)
        st->saw_sigusr1 = 1;

    return 0;
}

/* 프로세스 종료 시: 열려 있던 창 마감(보수적) — kprobe(do_exit) */
// SEC("kprobe/do_exit")
// int BPF_KPROBE(kp_do_exit)
// {
//     __u64 cg_id = bpf_get_current_cgroup_id();
//     if (!cg_id) return 0;

//     struct cg_state *st = bpf_map_lookup_elem(&cg_state_map, &cg_id);
//     struct cg_ctl   *ctl = bpf_map_lookup_elem(&cg_ctl_map, &cg_id);
//     if (st && ctl && st->in_runtime) {
//         if (st->saw_sigusr1) cnt_inc(CNT_CYCLE_OK);
//         else                 cnt_inc(CNT_CYCLE_FAIL);
//         st->in_runtime = 0;
//     }
//     return 0;
// }
