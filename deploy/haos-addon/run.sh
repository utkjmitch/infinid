#!/bin/sh
set -e
CONFIG=/data/options.json
SERIAL=$(jq -r '.serial' "$CONFIG")
MQTT_BROKER=$(jq -r '.mqtt_broker' "$CONFIG")
MQTT_USER=$(jq -r '.mqtt_user' "$CONFIG")
ZONE_NAMES=$(jq -r '.zone_names' "$CONFIG")
SAM=$(jq -r '.sam_reads' "$CONFIG")
CAPTURE=$(jq -r '.capture' "$CONFIG")
CAPTURE_MAX_MB=$(jq -r '.capture_max_mb' "$CONFIG")

mkdir -p /share/infinid
set -- -serial "$SERIAL" \
  -journal /share/infinid/events.jsonl \
  -rest ":8099"
# The 1 GiB default cap silently froze recording twice in the field —
# surface the knob so long captures can size it deliberately.
[ "$CAPTURE" = "true" ] && set -- "$@" -capture /share/infinid/capture.jsonl \
  -capture-max-mb "$CAPTURE_MAX_MB"
[ -n "$MQTT_BROKER" ] && set -- "$@" -mqtt-broker "$MQTT_BROKER" -mqtt-user "$MQTT_USER"
[ -n "$ZONE_NAMES" ] && set -- "$@" -zone-names "$ZONE_NAMES"
[ "$SAM" = "true" ] && set -- "$@" -sam

# Password travels via env, never argv (visible in process lists).
INFINID_MQTT_PASS=$(jq -r '.mqtt_password' "$CONFIG")
export INFINID_MQTT_PASS

exec /infinid "$@"
