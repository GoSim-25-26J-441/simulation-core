# Protocol buffers (gRPC API)

Generated Go packages live under **`gen/go/simulation/v1/`** and are **checked in** so that `go get github.com/GoSim-25-26J-441/simulation-core` works without running code generation.

## When to regenerate

After editing `proto/simulation/v1/simulation.proto` (or `buf.yaml` / `buf.gen.yaml`):

1. Install tools once (versions aligned with CI historically):

   ```bash
   make proto-deps
   ```

2. Generate:

   ```bash
   make proto
   # equivalent: buf generate
   ```

3. Commit the updated files under `gen/go/simulation/v1/`.

## Layout

- **Source:** `proto/simulation/v1/simulation.proto`
- **Buf config:** `buf.yaml`, `buf.gen.yaml` (output: `gen/go`, `paths=source_relative`)
- **Go import path:** `github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1`

The `go_package` option in `.proto` matches that import path.
