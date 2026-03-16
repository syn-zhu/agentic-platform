#!/bin/sh
# Guest init script — PID 1 inside the Firecracker VM.
#
# Mounts the rootfs, receives config from the executor over vsock,
# configures networking, writes injected files, and execs into the
# agent binary.
#
# Dependencies (in initramfs):
#   /bin/busybox  — sh, mount, mkdir, pivot_root, cat, sleep, etc.
#   /usr/sbin/ip  — full iproute2 (for onlink support)
#   /usr/bin/socat — vsock communication
#   /usr/bin/jq   — JSON parsing

set -e

log() { echo "init: $*"; }
die() { echo "init: FATAL: $*"; exit 1; }

# 1. Mount pseudo-filesystems.
log "mounting pseudo-filesystems"
mkdir -p /proc /sys /dev /dev/pts
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev
mount -t devpts devpts /dev/pts

# 2. Receive config from executor over vsock.
# The executor writes a single JSON object and closes the connection.
# CID 2 = host, port 10000 = control port.
log "connecting to host via vsock"
CONFIG=$(socat -T 5 - VSOCK-CONNECT:2:10000) || die "vsock connect failed"
log "received config from host"

# Parse config fields.
IP=$(echo "$CONFIG" | jq -r '.network.ip // empty')
GATEWAY=$(echo "$CONFIG" | jq -r '.network.gateway // empty')
PREFIX_LEN=$(echo "$CONFIG" | jq -r '.network.prefix_len // 32')
MTU=$(echo "$CONFIG" | jq -r '.network.mtu // 1500')

# 3. Wait for rootfs block device.
log "waiting for /dev/vda"
TRIES=0
while [ ! -e /dev/vda ] && [ "$TRIES" -lt 500 ]; do
    sleep 0.01
    TRIES=$((TRIES + 1))
done
[ -e /dev/vda ] || die "/dev/vda not found"

# 4. Mount rootfs as overlayfs (read-only lower + tmpfs upper).
log "mounting overlayfs"
mkdir -p /mnt/lower /mnt/upper /mnt/merged
mount -t ext4 -o ro /dev/vda /mnt/lower
mount -t tmpfs -o size=128M tmpfs /mnt/upper
mkdir -p /mnt/upper/upper /mnt/upper/work
mount -t overlay overlay \
    -o lowerdir=/mnt/lower,upperdir=/mnt/upper/upper,workdir=/mnt/upper/work \
    /mnt/merged

# 5. Configure network.
if [ -n "$IP" ]; then
    log "configuring network: ip=$IP/$PREFIX_LEN gw=$GATEWAY mtu=$MTU"
    ip addr add "$IP/$PREFIX_LEN" dev eth0
    ip link set eth0 up
    ip link set lo up
    [ "$MTU" != "1500" ] && ip link set eth0 mtu "$MTU"
    ip route add default via "$GATEWAY" onlink dev eth0
fi

# 6. Write injected files into the merged rootfs.
# The config contains a "files" array with {path, content_base64, mode}.
FILE_COUNT=$(echo "$CONFIG" | jq '.files | length')
if [ "$FILE_COUNT" -gt 0 ]; then
    log "writing $FILE_COUNT injected files"
    for i in $(seq 0 $((FILE_COUNT - 1))); do
        FILE_PATH=$(echo "$CONFIG" | jq -r ".files[$i].path")
        FILE_CONTENT=$(echo "$CONFIG" | jq -r ".files[$i].content_base64")
        FILE_MODE=$(echo "$CONFIG" | jq -r ".files[$i].mode // \"0644\"")
        FULL_PATH="/mnt/merged${FILE_PATH}"
        mkdir -p "$(dirname "$FULL_PATH")"
        echo "$FILE_CONTENT" | base64 -d > "$FULL_PATH"
        chmod "$FILE_MODE" "$FULL_PATH"
    done
fi

# 7. Switch root to the merged overlay.
log "switching root"
cd /mnt/merged
mkdir -p old_root
pivot_root . old_root
cd /
umount -l /old_root 2>/dev/null || true
rm -rf /old_root

# 8. Read image config and exec into agent.
if [ ! -f /etc/image-config.json ]; then
    die "/etc/image-config.json not found in rootfs"
fi

ENTRYPOINT=$(jq -r '.entrypoint | join(" ")' /etc/image-config.json)
AGENT_PORT=$(jq -r '.port // 8080' /etc/image-config.json)

# Set environment from image config.
eval "$(jq -r '.env // {} | to_entries[] | "export " + .key + "=\"" + .value + "\""' /etc/image-config.json 2>/dev/null)" || true
export PORT="$AGENT_PORT"

log "exec-ing into agent: $ENTRYPOINT"
exec $ENTRYPOINT
