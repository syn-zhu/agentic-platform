#!/bin/bash
set -euo pipefail

echo "=== Step 1: Install tools ==="
apt-get update -qq > /dev/null 2>&1
apt-get install -y -qq iproute2 curl > /dev/null 2>&1

echo "=== Step 2: Create veth pair simulating TAP ==="
# We use a veth pair named test-tap0 (host side) and guest-eth0 (guest side)
# to simulate the TAP device. The annotation watches for "test-tap0".
ip link add test-tap0 type veth peer name guest-eth0

# Host side: assign link-local IP, bring up
ip addr add 169.254.1.1/32 dev test-tap0
ip link set test-tap0 up

# Route to guest via test-tap0
ip route add 169.254.1.2/32 dev test-tap0

echo "=== Step 3: Create guest network namespace ==="
ip netns add guest
ip link set guest-eth0 netns guest

# Guest side: assign IP, bring up, add default route with onlink
ip netns exec guest ip addr add 169.254.1.2/32 dev guest-eth0
ip netns exec guest ip link set guest-eth0 up
ip netns exec guest ip link set lo up
ip netns exec guest ip route add default via 169.254.1.1 onlink dev guest-eth0

echo "=== Step 4: Verify connectivity host <-> guest ==="
echo "Pinging guest from host..."
ping -c 1 -W 2 169.254.1.2 && echo "  OK: host -> guest ping works" || echo "  WARN: host -> guest ping failed"

echo "Pinging host from guest..."
ip netns exec guest ping -c 1 -W 2 169.254.1.1 && echo "  OK: guest -> host ping works" || echo "  WARN: guest -> host ping failed"

echo "=== Step 5: Check iptables rule for test-tap0 ==="
iptables-legacy -t nat -L ISTIO_PRERT -n -v 2>/dev/null | grep test-tap0 && echo "  OK: reroute rule present" || echo "  FAIL: no reroute rule for test-tap0"

echo "=== Step 6: Send HTTP request from guest namespace ==="
echo "Attempting HTTP request to httpbin.org from guest netns..."
RESULT=$(ip netns exec guest curl -s -o /dev/null -w "%{http_code}" --connect-timeout 10 http://httpbin.org/get 2>&1) || true
echo "  HTTP status: $RESULT"

if [ "$RESULT" = "200" ]; then
    echo "  OK: request succeeded (traffic went through ztunnel)"
elif [ "$RESULT" = "000" ]; then
    echo "  INFO: connection failed — may need a ServiceEntry for httpbin.org"
    echo "  Trying a request to a known in-cluster service instead..."
    # Try reaching the kubernetes API (should be accessible)
    RESULT2=$(ip netns exec guest curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 -k https://kubernetes.default.svc:443/healthz 2>&1) || true
    echo "  K8s API status: $RESULT2"
else
    echo "  INFO: got HTTP $RESULT"
fi

echo "=== Step 7: Check ztunnel captured the traffic ==="
echo "Checking iptables packet counters on the reroute rule..."
iptables-legacy -t nat -L ISTIO_PRERT -n -v 2>/dev/null | head -5

echo ""
echo "=== Summary ==="
echo "If the reroute rule shows non-zero packet counts, ztunnel captured the traffic."
echo "Check ztunnel logs from outside: kubectl logs -n istio-system -l app=ztunnel --field-selector spec.nodeName=<node> | grep 169.254"

echo "=== Cleanup ==="
ip netns del guest 2>/dev/null || true
ip link del test-tap0 2>/dev/null || true

echo "=== Done ==="
