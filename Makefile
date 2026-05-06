BINARY := fleet
BUILD_DIR := build
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

.PHONY: build run clean test fmt install lint coverage deps vet setup proto proto-check proto-swift proto-swift-check

build:
	go build -v -ldflags "-s -w -X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY) ./cmd/fleet

run:
	go run ./cmd/fleet

clean:
	rm -rf $(BUILD_DIR)
	go clean

test:
	go test -race -v ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run ./...

COVERAGE_EXCLUDE := /(ui|cmd|chrome|debuglog|diagnostics|update)/

coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	@echo "\n--- All packages ---"
	@go tool cover -func=coverage.out | tail -1
	@grep -v -E '$(COVERAGE_EXCLUDE)' coverage.out > coverage-core.out
	@echo "--- Core packages (excl. UI, CLI, infra) ---"
	@go tool cover -func=coverage-core.out | tail -1

deps:
	go mod download

vet:
	go vet ./...

install: build
	cp $(BUILD_DIR)/$(BINARY) ~/.local/bin/

setup:
	pre-commit install

proto:
	buf generate

proto-check:
	@TMP=$$(mktemp -d) && trap 'rm -rf $$TMP' EXIT && \
		buf generate --output $$TMP && \
		diff -r $$TMP/gen/proto gen/proto > /dev/null && \
		echo "proto: gen/ is up to date" || \
		(echo "proto: gen/ is out of date — run 'make proto' and commit the result"; exit 1)

# Swift codegen for the Mac app's gRPC client. Requires `protoc-gen-swift` and
# `protoc-gen-grpc-swift-2` on $PATH (`brew install protoc-gen-grpc-swift`
# pulls both in plus protobuf). Output is committed under
# app/Sources/Fleet/DaemonClient/Generated/ — paralleling how Go's gen/ is
# committed — so consumers don't need protoc to build.
SWIFT_GEN_DIR := app/Sources/Fleet/DaemonClient/Generated

proto-swift:
	@command -v protoc >/dev/null || (echo "protoc not found — brew install protobuf"; exit 1)
	@command -v protoc-gen-swift >/dev/null || (echo "protoc-gen-swift not found — brew install swift-protobuf"; exit 1)
	@command -v protoc-gen-grpc-swift-2 >/dev/null || (echo "protoc-gen-grpc-swift-2 not found — brew install protoc-gen-grpc-swift"; exit 1)
	@mkdir -p $(SWIFT_GEN_DIR)
	protoc --proto_path=proto \
		--swift_out=$(SWIFT_GEN_DIR) \
		--swift_opt=Visibility=Public \
		--grpc-swift-2_out=$(SWIFT_GEN_DIR) \
		--grpc-swift-2_opt=Visibility=Public \
		--grpc-swift-2_opt=Client=true \
		--grpc-swift-2_opt=Server=false \
		proto/fleet/v1/fleet.proto

proto-swift-check:
	@TMP=$$(mktemp -d) && trap 'rm -rf $$TMP' EXIT && \
		protoc --proto_path=proto \
			--swift_out=$$TMP \
			--swift_opt=Visibility=Public \
			--grpc-swift-2_out=$$TMP \
			--grpc-swift-2_opt=Visibility=Public \
			--grpc-swift-2_opt=Client=true \
			--grpc-swift-2_opt=Server=false \
			proto/fleet/v1/fleet.proto && \
		diff -r $$TMP $(SWIFT_GEN_DIR) > /dev/null && \
		echo "proto-swift: $(SWIFT_GEN_DIR) is up to date" || \
		(echo "proto-swift: $(SWIFT_GEN_DIR) is out of date — run 'make proto-swift' and commit the result"; exit 1)
