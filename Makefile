BINARY   := axon
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -s -w -X github.com/hsiaosiyuan0/axon/cmd.Version=$(VERSION)
CGO_FLAGS := CGO_ENABLED=1 CGO_CFLAGS="-DSQLITE_ENABLE_FTS5"
BUILD    := go build -tags "fts5" -ldflags "$(LDFLAGS)"

LIBS_DIR  := $(CURDIR)/internal/libs

# macOS deployment target — must match the version libtokenizers.a was compiled for.
# Override with: make build-onnx-dev MACOSX_DEPLOYMENT_TARGET=15.0
MACOSX_DEPLOYMENT_TARGET ?= 15.5

# CGO flags for ONNX builds: point linker at internal/libs and set macOS target.
CGO_ONNX_FLAGS := CGO_ENABLED=1 \
	CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" \
	CGO_LDFLAGS="-L$(LIBS_DIR) -mmacosx-version-min=$(MACOSX_DEPLOYMENT_TARGET)" \
	MACOSX_DEPLOYMENT_TARGET=$(MACOSX_DEPLOYMENT_TARGET)

.PHONY: build build-onnx build-onnx-dev build-onnx-all run clean test install release \
        download-libs download-libs-all version

# ── Default build (fts5 only, no ONNX) ───────────────────────────────────────
build:
	$(CGO_FLAGS) $(BUILD) -o $(BINARY) .
	@echo "✅ Built $(BINARY) (fts5 only)"

# ── ONNX build — downloads all native libs automatically ─────────────────────
# No Rust required. Downloads pre-compiled libtokenizers.a + embeds libonnxruntime.
build-onnx:
	./scripts/build.sh --onnx
	@echo "✅ Built $(BINARY) (fts5 + onnx, embedded libonnxruntime)"

# ── ONNX dev build — uses already-downloaded libs, no script needed ───────────
# Requires: libtokenizers.a already in internal/libs/ (run build-onnx once first)
build-onnx-dev:
	$(CGO_ONNX_FLAGS) go build -tags "fts5 onnx" -ldflags "$(LDFLAGS)" -o $(BINARY) . 2>&1 | grep -v "ignoring duplicate"
	@echo "✅ Built $(BINARY) (fts5 + onnx, dev)"

# ── Download native libs for current platform only ───────────────────────────
download-libs:
	./scripts/build.sh --onnx --dry-run 2>/dev/null || true
	@mkdir -p internal/embed/assets internal/libs
	@bash -c '\
		GOOS=$$(go env GOOS); GOARCH=$$(go env GOARCH); \
		echo "▶ Downloading for $${GOOS}/$${GOARCH}..."; \
		source scripts/build.sh; \
	'

# ── Download native libs for ALL platforms (CI/release) ──────────────────────
download-libs-all:
	./scripts/build.sh --onnx --all-platforms

# ── Build ONNX binary for release (all platform assets embedded) ──────────────
build-onnx-all: download-libs-all
	$(CGO_ONNX_FLAGS) \
	go build -tags "fts5 onnx" -ldflags "$(LDFLAGS)" -o $(BINARY)-onnx . 2>&1 | grep -v "ignoring duplicate"
	@echo "✅ Built $(BINARY)-onnx with all platform assets embedded"

# ── Other targets ─────────────────────────────────────────────────────────────
install: build
	cp $(BINARY) /usr/local/bin/$(BINARY)

test:
	$(CGO_FLAGS) go test -tags "fts5" ./...

clean:
	rm -f $(BINARY) $(BINARY)-onnx
	rm -rf dist/
	rm -f internal/libs/libtokenizers*.a
	rm -f internal/embed/assets/onnxruntime-*.tgz
	rm -f internal/embed/model/model.onnx internal/embed/model/tokenizer.json

run:
	$(CGO_FLAGS) $(BUILD) -o $(BINARY) . && ./$(BINARY)

release:
	@mkdir -p dist
	GOOS=darwin  GOARCH=amd64 $(CGO_FLAGS) $(BUILD) -o dist/axon_$(VERSION)_darwin_amd64  .
	GOOS=darwin  GOARCH=arm64 $(CGO_FLAGS) $(BUILD) -o dist/axon_$(VERSION)_darwin_arm64  .
	GOOS=linux   GOARCH=amd64 $(CGO_FLAGS) $(BUILD) -o dist/axon_$(VERSION)_linux_amd64   .
	cd dist && sha256sum axon_$(VERSION)_* > SHA256SUMS.txt
	@echo "✅ Release binaries in dist/"

version:
	@echo $(VERSION)
