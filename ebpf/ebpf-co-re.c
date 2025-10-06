// hcbs_overrun.c
// Build:
//   cc -O2 -g -Wall hcbs_overrun.c -o hcbs_overrun -lbpf -lelf -lz
// Run:
//   sudo ./hcbs_overrun

#include <stdio.h>
#include <signal.h>
#include <unistd.h>
#include <errno.h>
#include <bpf/libbpf.h>
#include "hcbs_overrun.skel.h"   // bpftool gen skeleton 결과물 포함

static volatile sig_atomic_t stop;

static void on_sigint(int signo) { (void)signo; stop = 1; }

struct event {
    __u64 ts;
    __u32 cpu;
    __u32 pad;
    __s64 runtime_ns;
    __u32 _pad;
    __u64 tg_ptr;
    __u64 curr_cgid;
};

static int handle_event(void *ctx, void *data, size_t len)
{
    (void)ctx; (void)len;
    const struct event *e = data;
    printf("[ts=%llu ms] cpu=%u cgid=%llu runtime=%lld us\n",
           (unsigned long long)e->ts / 1000000,
           e->cpu,
           (unsigned long long)e->curr_cgid,
           (long long)e->runtime_ns / 1000);
    return 0;
}

int main(void)
{
    struct hcbs_overrun_bpf *skel = NULL;
    struct ring_buffer *rb = NULL;
    int err;

    libbpf_set_strict_mode(LIBBPF_STRICT_ALL);
    signal(SIGINT, on_sigint);
    signal(SIGTERM, on_sigint);

    skel = hcbs_overrun_bpf__open();
    if (!skel) {
        fprintf(stderr, "open skeleton failed\n");
        return 1;
    }

    err = hcbs_overrun_bpf__load(skel);
    if (err) {
        fprintf(stderr, "load skeleton failed: %d\n", err);
        goto out;
    }

    err = hcbs_overrun_bpf__attach(skel);
    if (err) {
        fprintf(stderr, "attach failed: %d\n", err);
        goto out;
    }

    rb = ring_buffer__new(bpf_map__fd(skel->maps.events), handle_event, NULL, NULL);
    if (!rb) {
        fprintf(stderr, "ring_buffer__new failed\n");
        goto out;
    }

    printf("HCBS DL-runtime exhaustion (first-hit per period) monitor running. Ctrl+C to stop.\n");
    while (!stop) {
        err = ring_buffer__poll(rb, 200);
        if (err == -EINTR) break;
        if (err < 0) {
            fprintf(stderr, "ring_buffer__poll: %d\n", err);
            break;
        }
    }

out:
    ring_buffer__free(rb);
    hcbs_overrun_bpf__destroy(skel);
    return 0;
}
