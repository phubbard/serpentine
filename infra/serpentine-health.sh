#!/bin/sh
# Health watch for serpentine-api, run from the Pi by serpentine-health.timer (ADR-037).
#
# Runs on webserver, not on axiom, deliberately: a watcher that shares a machine with the thing it
# watches goes quiet exactly when it matters. It checks two things, because they fail separately:
#
#   the LAN API  http://axiom:8990/v1/health   - the service itself, GraphHopper behind it
#   the public URL https://serpentine.phfactor.net/v1/health - Caddy, TLS, DNS, the WAN
#
# A rider only cares about the second, but the first says which half broke.
#
# Notifies ntfy (the same mechanism tgn-whisperer uses) on *state change* only, after
# FAIL_THRESHOLD consecutive failures, so one dropped packet doesn't wake anyone and a long outage
# doesn't send a hundred messages. Recovery sends one more.
#
# No rider data ever goes in a notification: status, version and which check failed, nothing else.
# The topic is public and guessable, so treat anything sent here as world-readable.

set -u

NTFY_TOPIC="${NTFY_TOPIC:-https://ntfy.sh/serpentine-alerts}"
LAN_URL="${LAN_URL:-http://axiom.phfactor.net:8990/v1/health}"
PUBLIC_URL="${PUBLIC_URL:-https://serpentine.phfactor.net/v1/health}"
STATE_DIR="${STATE_DIR:-$HOME/.local/state/serpentine-health}"
STATE="$STATE_DIR/state"
FAIL_THRESHOLD="${FAIL_THRESHOLD:-3}"   # ~6 minutes at a 2-minute timer
TIMEOUT="${TIMEOUT:-10}"

mkdir -p "$STATE_DIR"

notify() {
	# notify <title> <priority> <tags> <body>
	curl -fsS --max-time 15 \
		-H "Title: $1" -H "Priority: $2" -H "Tags: $3" \
		-d "$4" "$NTFY_TOPIC" >/dev/null 2>&1 \
		|| logger -t serpentine-health "ntfy POST failed"
}

# check <url> -> echoes a failure reason, or nothing when healthy.
check() {
	body=$(curl -fsS --max-time "$TIMEOUT" "$1" 2>&1) || {
		echo "unreachable: $(echo "$body" | tail -1 | cut -c1-120)"
		return
	}
	echo "$body" | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception as e:
    print("unreadable response: %s" % e); raise SystemExit
if not d.get("ok"):
    print("reports not ok"); raise SystemExit
# Charger data going stale is a silent degradation: plans still work, charge stops quietly stop
# appearing. Worth knowing about before a rider notices.
if not d.get("chargers"):
    print("charger data unavailable"); raise SystemExit
'
}

if [ "${1:-}" = "--test" ]; then
	notify "Serpentine health check" "default" "white_check_mark" \
		"Test message from $(hostname). The watch is installed and can reach ntfy."
	echo "test notification sent to $NTFY_TOPIC"
	exit 0
fi

reason=""
lan=$(check "$LAN_URL")
[ -n "$lan" ] && reason="API ($lan)"
pub=$(check "$PUBLIC_URL")
if [ -n "$pub" ]; then
	[ -n "$reason" ] && reason="$reason; public ($pub)" || reason="public URL ($pub)"
fi

fails=0
alerted=no
[ -f "$STATE" ] && . "$STATE"

if [ -n "$reason" ]; then
	fails=$((fails + 1))
	if [ "$fails" -ge "$FAIL_THRESHOLD" ] && [ "$alerted" = "no" ]; then
		notify "Serpentine is down" "high" "rotating_light" \
			"$reason
Failed $fails checks in a row. make -C server logs on axiom."
		alerted=yes
	fi
else
	if [ "$alerted" = "yes" ]; then
		notify "Serpentine is back" "default" "white_check_mark" \
			"Both the API and the public URL are answering again."
	fi
	fails=0
	alerted=no
fi

printf 'fails=%s\nalerted=%s\n' "$fails" "$alerted" > "$STATE"
