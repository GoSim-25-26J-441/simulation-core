#!/usr/bin/env bash
# Poll AWS SSM Run Command until a terminal status for one instance, then print output.
#
# Usage: wait-ssm-command.sh COMMAND_ID INSTANCE_ID [REGION]
# Env:   MAX_WAIT_SEC (default: 600)
#
# Exit 0 only if Status is Success; otherwise exits 1 and prints invocation details.

set -euo pipefail

CMD_ID="${1:?command id required}"
IID="${2:?instance id required}"
REGION="${3:-${AWS_REGION:-}}"
if [ -z "${REGION}" ]; then
  echo "usage: $0 COMMAND_ID INSTANCE_ID [REGION] (or set AWS_REGION)" >&2
  exit 2
fi

MAX_WAIT_SEC="${MAX_WAIT_SEC:-600}"
deadline=$((SECONDS + MAX_WAIT_SEC))

echo "Waiting for SSM command ${CMD_ID} on ${IID} (region=${REGION}, max ${MAX_WAIT_SEC}s)..."

while (( SECONDS < deadline )); do
  status=""
  if ! status="$(aws ssm get-command-invocation \
      --command-id "${CMD_ID}" \
      --instance-id "${IID}" \
      --region "${REGION}" \
      --query 'Status' \
      --output text 2>/dev/null)"; then
    sleep 2
    continue
  fi

  case "${status}" in
    Pending|InProgress|Delayed|"")
      sleep 2
      ;;
    Success)
      echo "--- SSM stdout ---"
      aws ssm get-command-invocation \
        --command-id "${CMD_ID}" \
        --instance-id "${IID}" \
        --region "${REGION}" \
        --query 'StandardOutputContent' \
        --output text
      echo "--- SSM stderr ---"
      aws ssm get-command-invocation \
        --command-id "${CMD_ID}" \
        --instance-id "${IID}" \
        --region "${REGION}" \
        --query 'StandardErrorContent' \
        --output text
      exit 0
      ;;
    *)
      echo "SSM command failed with status: ${status}" >&2
      aws ssm get-command-invocation \
        --command-id "${CMD_ID}" \
        --instance-id "${IID}" \
        --region "${REGION}" \
        --output json >&2 || true
      exit 1
      ;;
  esac
done

echo "Timed out after ${MAX_WAIT_SEC}s waiting for SSM command ${CMD_ID}" >&2
exit 1
