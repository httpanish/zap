#!/usr/bin/env bash
# The pre-release shaped-link check from the specification. It shapes outgoing
# traffic on an interface with tc tbf, runs zap, and checks that the upload figure
# is within 5% of the shaped rate.
#
# Usage: sudo scripts/shaped-link-check.sh <interface> [path-to-zap]
#
# This replaces the interface's root qdisc while it runs, slowing all traffic on
# it, and deletes the qdisc on exit. It needs a link faster than 100 Mbps upstream
# to test every rate. It runs against Cloudflare, not a local server.
set -euo pipefail

iface=${1:?usage: shaped-link-check.sh <interface> [path-to-zap]}
zap=${2:-./zap}

cleanup() { tc qdisc del dev "$iface" root 2>/dev/null || true; }
trap cleanup EXIT

failed=0
for rate in 5 20 100; do
	tc qdisc replace dev "$iface" root tbf rate "${rate}mbit" burst 64kb latency 400ms
	upload=$("$zap" --json --no-save | jq -r '.upload.mbps')
	cleanup
	if awk -v got="$upload" -v want="$rate" 'BEGIN { d = got - want; if (d < 0) d = -d; exit !(d <= want * 0.05) }'; then
		echo "pass: shaped to $rate Mbps, measured $upload Mbps"
	else
		echo "FAIL: shaped to $rate Mbps, measured $upload Mbps"
		failed=1
	fi
done
exit "$failed"
