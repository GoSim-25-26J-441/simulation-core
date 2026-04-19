# Optional: pin BUF_VERSION in CI or locally if buf behavior changes.
BUF ?= buf

.PHONY: proto
proto:
	$(BUF) generate

.PHONY: proto-deps
proto-deps:
	go install github.com/bufbuild/buf/cmd/buf@v1.62.1
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
