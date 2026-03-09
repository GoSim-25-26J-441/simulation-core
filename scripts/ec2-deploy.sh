#!/usr/bin/env bash
# Deploy simulation-core binary from S3 to this host and restart the service.
# Same pattern as go-sim-backend: invoked by GitHub Actions via SSM Run Command
# with: /tmp/deploy.sh BUCKET REGION
#
# Usage: ./ec2-deploy.sh BUCKET REGION
# Optional env: HTTP_PORT (default 8080), GRPC_PORT (default 50051),
#               INSTALL_DIR (default /opt/simulation-core)

set -euo pipefail

BUCKET="${1:?Usage: $0 BUCKET REGION}"
REGION="${2:?Usage: $0 BUCKET REGION}"
INSTALL_DIR="${INSTALL_DIR:-/opt/simulation-core}"
HTTP_PORT="${HTTP_PORT:-8080}"
GRPC_PORT="${GRPC_PORT:-50051}"

BINARY_URL="s3://${BUCKET}/simulation-core/simd"
TARGET="${INSTALL_DIR}/simd"
TMP="/tmp/simd.new"

echo "[1/3] Downloading binary from ${BINARY_URL}..."
aws s3 cp "${BINARY_URL}" "${TMP}" --region "${REGION}"
chmod +x "${TMP}"

echo "[2/3] Installing to ${TARGET}..."
mkdir -p "${INSTALL_DIR}"
if [ -f "${TARGET}" ]; then
  cp -a "${TARGET}" "${TARGET}.bak"
fi
mv "${TMP}" "${TARGET}"

echo "[3/3] Restarting simulation-core (http :${HTTP_PORT}, grpc :${GRPC_PORT})..."
if command -v systemctl &>/dev/null && systemctl is-active --quiet simulation-core 2>/dev/null; then
  systemctl restart simulation-core
  echo "systemctl restart simulation-core done."
elif [ -n "${SIMD_PIDFILE:-}" ] && [ -f "$SIMD_PIDFILE" ]; then
  old_pid=$(cat "$SIMD_PIDFILE")
  [ -n "$old_pid" ] && kill "$old_pid" 2>/dev/null || true
  sleep 2
  nohup "${TARGET}" -http-addr ":${HTTP_PORT}" -grpc-addr ":${GRPC_PORT}" >> "${INSTALL_DIR}/simd.log" 2>&1 &
  echo $! > "$SIMD_PIDFILE"
  echo "Started simd via PID file."
else
  pkill -f "simd.*-http-addr" 2>/dev/null || true
  sleep 2
  nohup "${TARGET}" -http-addr ":${HTTP_PORT}" -grpc-addr ":${GRPC_PORT}" >> "${INSTALL_DIR}/simd.log" 2>&1 &
  echo "Started simd (no systemd or PID file). PID: $!"
fi

echo "Deploy complete."
