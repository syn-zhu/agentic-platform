//go:build ignore

package proxy

// Generate Go bindings for the eBPF connect4 program.
// Run: go generate ./internal/proxy/
//
// Requires: clang, llvm-strip, and libbpf headers.
// On the build machine (not macOS): apt-get install clang llvm libbpf-dev

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 connect4 ../../bpf/connect4.c
