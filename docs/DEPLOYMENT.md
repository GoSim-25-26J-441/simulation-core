# Deployment

Simulation-core is deployed via **GitHub Actions** on push to `main`. No application secrets are required; HTTP and gRPC ports are passed at runtime.

## CI workflow

- **File:** [.github/workflows/ci.yml](../.github/workflows/ci.yml)
- **Triggers:** Push and pull requests to `main`, `dev`, and `ci`.
- **Test job:** Build, vet, and test (no integration tag by default).
- **Deploy job:** Runs only on push to `main` after tests pass. Builds a Linux amd64 binary, uploads it to S3, uploads the deploy script, then runs the script on the target EC2 instance via AWS SSM.

## S3 layout

| Path | Description |
|------|-------------|
| `s3://<bucket>/simulation-core/simd` | Linux binary (built from `./cmd/simd`) |
| `s3://<bucket>/simulation-core/scripts/deploy.sh` | EC2 deploy script (from `scripts/ec2-deploy.sh`) |

## Deploy script (EC2)

The script [scripts/ec2-deploy.sh](../scripts/ec2-deploy.sh) follows the same pattern as go-sim-backend: it is uploaded to S3 and triggered on the instance via **SSM Run Command** (document `AWS-RunShellScript`). The workflow downloads it to `/tmp/deploy.sh`, makes it executable, and runs `/tmp/deploy.sh BUCKET REGION`. The script:

1. Downloads the `simd` binary from S3.
2. Installs it to a fixed path (default `/opt/simulation-core/simd`).
3. Restarts the process with runtime ports: `-http-addr :${HTTP_PORT:-8080}` and `-grpc-addr :${GRPC_PORT:-50051}`.

Optional environment variables on the EC2 host: `HTTP_PORT`, `GRPC_PORT`, `INSTALL_DIR`. The script supports systemd (`simulation-core` service), a PID file (`SIMD_PIDFILE`), or a fallback (nohup).

## GitHub secrets (for deploy job)

| Secret | Purpose |
|--------|---------|
| `AWS_REGION` | AWS region for S3 and SSM |
| `AWS_ROLE_ARN` | OIDC role for GitHub Actions to assume |
| `DEPLOY_BUCKET` | S3 bucket for binary and deploy script |
| `EC2_INSTANCE_ID` | EC2 instance ID for SSM Run Command |
| `HEALTH_CHECK_URL` | (Optional) Full URL for health check after deploy (e.g. `https://host/healthz`). If unset, health check is skipped. |

## Health endpoint

The HTTP server exposes `/healthz`. Configure `HEALTH_CHECK_URL` to point at that endpoint (e.g. `https://sim.example.com/healthz`) to enable post-deploy health checks in the workflow.
