# Branch Protection Setup

This document explains how to configure branch protection rules to enforce CI checks before merging PRs.

## Quick Setup

To enable branch protection and require CI checks to pass before merging:

### 1. Navigate to Branch Protection Settings

1. Go to your GitHub repository
2. Click **Settings** → **Branches** (left sidebar)
3. Under "Branch protection rules", click **Add rule**

### 2. Configure Protection Rule

**Branch name pattern:** Enter the branch you want to protect (e.g., `main`, `develop`, or `*` for all branches)

**Enable the following settings:**

#### Protect matching branches
- ✅ **Require a pull request before merging**
  - ✅ Require approvals: 1 (or more, as needed)
  - ✅ Dismiss stale pull request approvals when new commits are pushed
  - ✅ Require review from Code Owners (optional)

- ✅ **Require status checks to pass before merging**
  - ✅ Require branches to be up to date before merging
  - **Select the following status checks:**
    - `test (1.25.x)` - Tests on Go 1.25.x
    - `lint` - Linting checks
    - `build` - Build verification
    - `status-check` - Overall status verification

- ✅ **Require conversation resolution before merging**

- ✅ **Require linear history** (optional but recommended)

- ✅ **Include administrators** (apply rules to admins too)

#### Do NOT enable (unless specifically needed)
- ❌ Require deployments to succeed before merging
- ❌ Lock branch
- ❌ Allow force pushes
- ❌ Allow deletions

### 3. Save Changes

Click **Create** or **Save changes** at the bottom of the page.

## CI Pipeline Details

The CI pipeline (`.github/workflows/pr-tests.yml`) runs the following checks on every PR:

### Test Job (runs on Go 1.25.x)
- ✅ Code formatting check (`gofmt`)
- ✅ Go vet static analysis
- ✅ All tests with race detection
- ✅ Coverage calculation
- ✅ Coverage threshold enforcement (minimum 80%)

### Lint Job
- ✅ golangci-lint with multiple linters
- ✅ Security checks (gosec)
- ✅ Code quality checks
- ✅ Best practices enforcement

### Build Job
- ✅ Verify all packages build successfully
- ✅ Build all cmd binaries

### Status Check Job
- ✅ Aggregates results from all jobs
- ✅ Fails if any job fails

## Coverage Requirements

The CI pipeline enforces a **minimum 80% code coverage** threshold. PRs with coverage below this threshold will fail the checks.

To check coverage locally:
```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

To view HTML coverage report:
```bash
go tool cover -html=coverage.out
```

## Bypassing Checks (Emergency Only)

In emergency situations, repository admins can:
1. Temporarily disable branch protection
2. Merge the PR
3. Re-enable branch protection

**Note:** This should only be done in critical situations and with proper justification.

## Testing the Pipeline Locally

Before pushing your PR, run these commands locally:

```bash
# Format code
gofmt -w .

# Run vet
go vet ./...

# Run tests
go test -v -race ./...

# Check coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | grep total

# Run linter (if golangci-lint is installed)
golangci-lint run
```

## Installing golangci-lint Locally

```bash
# macOS/Linux
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin

# Or using go install
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

## Troubleshooting

### Tests fail in CI but pass locally
- Ensure you're using the same Go version
- Check for race conditions (run `go test -race ./...`)
- Verify all dependencies are committed

### Coverage below threshold
- Add more test cases for uncovered code
- Check coverage report: `go tool cover -html=coverage.out`
- Focus on critical paths and error handling

### Lint failures
- Run `golangci-lint run` locally
- Fix reported issues
- Some issues can be auto-fixed: `golangci-lint run --fix`

### Build failures
- Ensure all imports are available
- Run `go mod tidy` to clean up dependencies
- Check for syntax errors

## PR Checklist

Before submitting a PR, ensure:
- [ ] Code is formatted (`gofmt -w .`)
- [ ] All tests pass (`go test ./...`)
- [ ] Coverage is above 80%
- [ ] No linting errors (`golangci-lint run`)
- [ ] Build succeeds (`go build ./...`)
- [ ] Commit messages are clear
- [ ] PR description explains changes

## Additional Resources

- [GitHub Branch Protection Documentation](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches)
- [golangci-lint Documentation](https://golangci-lint.run/)
- [Go Testing Best Practices](https://go.dev/doc/tutorial/add-a-test)
