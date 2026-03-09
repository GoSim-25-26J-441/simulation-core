#!/usr/bin/env bash
# Deploy simulation-core binary from S3 to /opt/simulation-core and restart the service.
#
# Usage: ./ec2-deploy.sh BUCKET REGION

set -euo pipefail

BUCKET="${1:?Usage: $0 BUCKET REGION}"
REGION="${2:?Usage: $0 BUCKET REGION}"

INSTALL_DIR="/opt/simulation-core"
TARGET="${INSTALL_DIR}/simd"

echo "[1/2] Downloading binary and installing to ${INSTALL_DIR}..."
mkdir -p "${INSTALL_DIR}"
aws s3 cp "s3://${BUCKET}/simulation-core/simd" "${TARGET}" --region "${REGION}"
chmod +x "${TARGET}"

echo "[2/2] Restarting simulation-core..."
systemctl restart simulation-core

echo "Deploy complete."
