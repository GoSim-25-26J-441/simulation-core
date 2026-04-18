#!/usr/bin/env bash
# Install or refresh the systemd unit for simulation-core (simd).
# Idempotent: safe to run before every deploy bootstrap.
#
# Usage: install-simulation-core-service.sh
#
# Expects root (default for AWS SSM Run Command). Override paths with:
#   INSTALL_DIR — installation directory (default: /opt/simulation-core)

set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/simulation-core}"
BIN="${INSTALL_DIR}/simd"
UNIT_PATH="/etc/systemd/system/simulation-core.service"

mkdir -p "${INSTALL_DIR}"

cat >"${UNIT_PATH}" <<EOF
[Unit]
Description=simulation-core SIMD HTTP/gRPC server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${INSTALL_DIR}
ExecStart=${BIN} -http-addr :8082 -grpc-addr :50051 -log-level info
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

chmod 644 "${UNIT_PATH}"
systemctl daemon-reload
systemctl enable simulation-core.service

echo "systemd unit installed at ${UNIT_PATH} (enabled). Binary expected at ${BIN}."
