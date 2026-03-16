# Executor Dockerfile
#
# Two-stage build:
#   1. Build executor binary and init binary (static, for initramfs)
#   2. Pack init into initramfs cpio+lz4, combine with kernel into runtime image
#
# The kernel (vmlinux) must be provided as a build context file or
# copied from a base image. For the POC, we copy it from the existing
# executor image.

FROM 268558157000.dkr.ecr.us-east-1.amazonaws.com/agentic-platform/executor:latest AS kernel-source

# ---------------------------------------------------------------------------
# Build stage: compile Go binaries
# ---------------------------------------------------------------------------
FROM golang:1.25-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Build executor binary (dynamically linked is fine, runs in container).
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/executor ./cmd/executor

# Build init binary (must be static — runs as PID 1 in a minimal initramfs).
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags='-extldflags=-static' -o /out/init ./cmd/init

# ---------------------------------------------------------------------------
# Initramfs stage: pack init binary into cpio+lz4 archive
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim AS initramfs

RUN apt-get update -qq && apt-get install -y -qq cpio lz4 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/init /tmp/init
RUN mkdir -p /staging /out && cp /tmp/init /staging/init && chmod 755 /staging/init && \
    cd /staging && find . -print0 | cpio --null -o --format=newc 2>/dev/null | lz4 -l -9 > /out/initramfs.cpio.lz4

# ---------------------------------------------------------------------------
# Runtime stage
# ---------------------------------------------------------------------------
FROM debian:bookworm-slim

RUN apt-get update -qq && apt-get install -y -qq \
    iproute2 iptables \
    && rm -rf /var/lib/apt/lists/*

# Kernel and Firecracker binary from the existing executor image.
COPY --from=kernel-source /opt/firecracker/vmlinux /opt/firecracker/vmlinux
COPY --from=kernel-source /usr/bin/firecracker /usr/bin/firecracker

# Our initramfs (with our init binary).
COPY --from=initramfs /out/initramfs.cpio.lz4 /opt/firecracker/initramfs.cpio.lz4

# Executor binary.
COPY --from=builder /out/executor /usr/bin/executor

ENTRYPOINT ["/usr/bin/executor"]
