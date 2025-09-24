package main

// BTF를 만들지 않도록 -g0, vmlinux.h 인클루드는 -I.
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -tags linux -cflags "-I. -g0" ebpf ebpf.c

// 혹시 남아있을지도 모르는 .BTF/.BTF.ext 섹션 제거 (아키텍처별 .o 둘 다 시도)
///go:generate bash -c 'command -v llvm-objcopy >/dev/null && test -f ebpf_bpfel.o && llvm-objcopy --remove-section .BTF --remove-section .BTF.ext ebpf_bpfel.o || true'
///go:generate bash -c 'command -v llvm-objcopy >/dev/null && test -f ebpf_bpfeb.o && llvm-objcopy --remove-section .BTF --remove-section .BTF.ext ebpf_bpfeb.o || true'