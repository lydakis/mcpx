.PHONY: build test vet check qa perf perf-loop dist release-snapshot clean

BINARY ?= mcpx
CMD ?= ./cmd/mcpx
DIST_DIR ?= dist
PLATFORMS ?= darwin/arm64 darwin/amd64 linux/amd64 linux/arm64

build:
	go build -o $(BINARY) $(CMD)

test:
	go test ./...

vet:
	go vet ./...

check: test vet build

qa:
	./scripts/qa_matrix.sh

perf:
	./scripts/perf_bench.sh

perf-loop:
	./scripts/perf_cli_loop.sh

dist:
	rm -rf "$(DIST_DIR)"
	mkdir -p "$(DIST_DIR)"
	@set -e; \
	for platform in $(PLATFORMS); do \
		os="$${platform%/*}"; \
		arch="$${platform#*/}"; \
		out="$(DIST_DIR)/mcpx-$${os}-$${arch}"; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" go build -ldflags="-s -w" -o "$$out" $(CMD); \
	done
	cd "$(DIST_DIR)" && shasum -a 256 mcpx-* > SHA256SUMS

release-snapshot:
	goreleaser release --snapshot --clean

clean:
	rm -f "$(BINARY)"
