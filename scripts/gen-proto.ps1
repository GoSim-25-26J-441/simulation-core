$ErrorActionPreference = "Stop"

# Generate Go code from protobuf definitions using buf.
#
# Prereqs:
#   - Go toolchain installed
#   - buf installed: https://buf.build/docs/installation
#
# Output:
#   - Generated code under ./gen/go (gitignored by default)

buf generate


