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
| `s3://<bucket>/simulation-core/scripts/install-service.sh` | Creates `simulation-core.service` and `systemctl enable` ([scripts/install-simulation-core-service.sh](../scripts/install-simulation-core-service.sh)) |
| `s3://<bucket>/simulation-core/scripts/deploy.sh` | Downloads binary from S3 and `systemctl restart simulation-core` ([scripts/ec2-deploy.sh](../scripts/ec2-deploy.sh)) |

## Deploy scripts (EC2)

1. **Install (idempotent, once per host or after unit changes):** [scripts/install-simulation-core-service.sh](../scripts/install-simulation-core-service.sh) writes `/etc/systemd/system/simulation-core.service`, runs `daemon-reload`, and `enables` the unit. Default `ExecStart` uses `-http-addr :8080` and `-grpc-addr :50051` (see `./cmd/simd` flags).
2. **Deploy:** [scripts/ec2-deploy.sh](../scripts/ec2-deploy.sh) downloads the binary to `/opt/simulation-core/simd` and runs `systemctl restart simulation-core`.

The GitHub Actions deploy job downloads both scripts via SSM, runs install then deploy in one shell command, and **waits for success by polling SSM command status** ([scripts/wait-ssm-command.sh](../scripts/wait-ssm-command.sh)) using the returned command id — not an HTTP health check.

Optional on the instance: set `INSTALL_DIR` when running `install-simulation-core-service.sh` if the unit and binary path should differ from `/opt/simulation-core`.

## GitHub secrets (for deploy job)

| Secret | Purpose |
|--------|---------|
| `AWS_REGION` | AWS region for S3 and SSM |
| `AWS_ROLE_ARN` | OIDC role for GitHub Actions to assume |
| `DEPLOY_BUCKET` | S3 bucket for binary and deploy script |
| `EC2_INSTANCE_ID` | EC2 instance ID for SSM Run Command |

## Verifying deploys

The workflow treats the deploy as successful when **SSM Run Command** reports `Success` for that invocation. For manual checks, the HTTP server exposes `/healthz` if you want to probe the instance yourself.
