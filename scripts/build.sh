#!/usr/bin/env bash
# scripts/build.sh — Build axon with optional ONNX + embedded native libs
#
# Usage:
#   ./scripts/build.sh              # basic build (fts5 only)
#   ./scripts/build.sh --onnx       # ONNX build — downloads libonnxruntime & libtokenizers
#   ./scripts/build.sh --onnx --all-platforms  # Download assets for all platforms (CI use)
#
# What --onnx does:
#   1. Downloads libonnxruntime-{platform}.tgz  → internal/embed/assets/
#   2. Downloads libtokenizers.{platform}.tar.gz → extracts libtokenizers.a to internal/libs/
#   3. Compiles axon with -tags "fts5 onnx"
#
# No Rust toolchain required. No manual downloads. Zero extra steps for users.
set -euo pipefail

# ── Config ────────────────────────────────────────────────────────────────────
BINARY="axon"
ORT_VERSION="1.21.0"
TOKENIZERS_VERSION="v1.25.0"

# Built-in model: Xenova/bge-small-zh-v1.5 (quantized, ~24 MB)
BUILTIN_MODEL_REPO="Xenova/bge-small-zh-v1.5"
BUILTIN_MODEL_ONNX="onnx/model_quantized.onnx"
BUILTIN_MODEL_TOKENIZER="tokenizer.json"
BUILTIN_MODEL_MIRROR="${BUILTIN_MODEL_MIRROR:-https://huggingface.co}"  # override with HF_MIRROR env

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSETS_DIR="${REPO_ROOT}/internal/embed/assets"
LIBS_DIR="${REPO_ROOT}/internal/libs"
MODEL_DIR="${REPO_ROOT}/internal/embed/model"

BUILD_TAGS="fts5"
ONNX_BUILD=false
ALL_PLATFORMS=false

# ── Parse args ────────────────────────────────────────────────────────────────
for arg in "$@"; do
  case $arg in
    --onnx)          ONNX_BUILD=true ;;
    --all-platforms) ALL_PLATFORMS=true ;;
    --help|-h)
      echo "Usage: $0 [--onnx] [--all-platforms]"
      echo "  --onnx            Build with embedded ONNX Runtime + tokenizers (no Rust needed)"
      echo "  --all-platforms   Download assets for all supported platforms (CI use)"
      exit 0
      ;;
  esac
done

# ── Helpers ───────────────────────────────────────────────────────────────────
info()  { echo "  ✅ $*"; }
warn()  { echo "  ⚠️  $*"; }
error() { echo "  ❌ $*" >&2; exit 1; }
step()  { echo; echo "▶ $*"; }

# download_file <url> <dest>
download_file() {
  local url="$1"
  local dest="$2"
  if [[ -f "$dest" ]]; then
    info "Already exists: $(basename "$dest"), skipping download"
    return 0
  fi
  echo "  ↓ $(basename "$dest") ..."
  if command -v curl &>/dev/null; then
    curl -fsSL --progress-bar -o "$dest" "$url" || error "Download failed: $url"
  elif command -v wget &>/dev/null; then
    wget -q --show-progress -O "$dest" "$url" || error "Download failed: $url"
  else
    error "Neither curl nor wget found. Please install one."
  fi
  info "Downloaded: $(basename "$dest")"
}

# ── Detect current platform ───────────────────────────────────────────────────
detect_platform() {
  local goos goarch
  goos="$(go env GOOS)"
  goarch="$(go env GOARCH)"
  echo "${goos}/${goarch}"
}

# ── ORT platform string ───────────────────────────────────────────────────────
ort_platform() {
  local goos="$1" goarch="$2"
  case "${goos}/${goarch}" in
    darwin/arm64)  echo "osx-arm64" ;;
    darwin/amd64)  echo "osx-x86_64" ;;
    linux/amd64)   echo "linux-x64" ;;
    linux/arm64)   echo "linux-aarch64" ;;
    windows/amd64) echo "win-x64" ;;
    *) error "Unsupported platform for ORT: ${goos}/${goarch}" ;;
  esac
}

# ── Tokenizers platform string ────────────────────────────────────────────────
tokenizers_platform() {
  local goos="$1" goarch="$2"
  case "${goos}/${goarch}" in
    darwin/arm64)  echo "darwin-arm64" ;;
    darwin/amd64)  echo "darwin-x86_64" ;;
    linux/amd64)   echo "linux-x86_64" ;;
    linux/arm64)   echo "linux-arm64" ;;
    *) error "Unsupported platform for tokenizers: ${goos}/${goarch}" ;;
  esac
}

# ── Download ORT archive for a platform ──────────────────────────────────────
download_ort() {
  local goos="$1" goarch="$2"
  local platform
  platform="$(ort_platform "$goos" "$goarch")"
  local archive="onnxruntime-${platform}-${ORT_VERSION}.tgz"
  local url="https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${archive}"
  download_file "$url" "${ASSETS_DIR}/${archive}"
}

# ── Download & extract libtokenizers.a for a platform ────────────────────────
download_tokenizers() {
  local goos="$1" goarch="$2"
  local platform
  platform="$(tokenizers_platform "$goos" "$goarch")"
  local archive="libtokenizers.${platform}.tar.gz"
  local url="https://github.com/daulet/tokenizers/releases/download/${TOKENIZERS_VERSION}/${archive}"
  local tmp="${LIBS_DIR}/${archive}"

  # Determine output .a name (platform-suffixed so all-platforms mode can coexist)
  local libname="libtokenizers.a"
  if $ALL_PLATFORMS; then
    libname="libtokenizers.${platform}.a"
  fi
  local dest="${LIBS_DIR}/${libname}"

  if [[ -f "$dest" ]]; then
    info "Already exists: ${libname}, skipping download"
    return 0
  fi

  download_file "$url" "$tmp"

  echo "  📦 Extracting ${libname} ..."
  # The archive contains libtokenizers.a at the root
  tar -xzf "$tmp" -C "$LIBS_DIR" libtokenizers.a 2>/dev/null || \
  tar -xzf "$tmp" -C "$LIBS_DIR" 2>/dev/null
  # Rename if needed
  if [[ "$libname" != "libtokenizers.a" && -f "${LIBS_DIR}/libtokenizers.a" ]]; then
    mv "${LIBS_DIR}/libtokenizers.a" "$dest"
  fi
  rm -f "$tmp"
  info "Extracted: ${libname}"
}

# ── Step 0: Check Go ──────────────────────────────────────────────────────────
step "Checking Go toolchain"
command -v go &>/dev/null || error "Go not found. Install from https://go.dev/dl/"
info "$(go version)"

# ── Step 1: Download native libraries ─────────────────────────────────────────
if $ONNX_BUILD; then
  BUILD_TAGS="fts5 onnx"

  mkdir -p "$ASSETS_DIR" "$LIBS_DIR"

  GOOS="$(go env GOOS)"
  GOARCH="$(go env GOARCH)"

  if $ALL_PLATFORMS; then
    step "Downloading ORT archives for all platforms → internal/embed/assets/"
    for pair in "darwin arm64" "darwin amd64" "linux amd64" "linux arm64"; do
      os="${pair%% *}"; arch="${pair##* }"
      download_ort "$os" "$arch"
    done

    step "Downloading libtokenizers for all platforms → internal/libs/"
    for pair in "darwin arm64" "darwin amd64" "linux amd64" "linux arm64"; do
      os="${pair%% *}"; arch="${pair##* }"
      download_tokenizers "$os" "$arch"
    done
  else
    step "Downloading ORT for ${GOOS}/${GOARCH} → internal/embed/assets/"
    download_ort "$GOOS" "$GOARCH"

    step "Downloading libtokenizers for ${GOOS}/${GOARCH} → internal/libs/"
    download_tokenizers "$GOOS" "$GOARCH"
  fi

  # Verify libtokenizers.a exists
  if [[ ! -f "${LIBS_DIR}/libtokenizers.a" ]]; then
    error "libtokenizers.a not found in ${LIBS_DIR}"
  fi

  # ── Step 1b: Download built-in model files for embedding ─────────────────
  step "Downloading built-in model (bge-small-zh-v1.5 quantized, ~24 MB) → internal/embed/model/"
  mkdir -p "$MODEL_DIR"

  MODEL_ONNX_DEST="${MODEL_DIR}/model.onnx"
  MODEL_TOK_DEST="${MODEL_DIR}/tokenizer.json"

  MIRROR_BASE="${BUILTIN_MODEL_MIRROR}"
  # Support HF_MIRROR env for China users (e.g. https://hf-mirror.com)
  if [[ -n "${HF_MIRROR:-}" ]]; then
    MIRROR_BASE="${HF_MIRROR}"
    info "Using custom mirror: ${MIRROR_BASE}"
  fi

  MODEL_ONNX_URL="${MIRROR_BASE}/${BUILTIN_MODEL_REPO}/resolve/main/${BUILTIN_MODEL_ONNX}"
  MODEL_TOK_URL="${MIRROR_BASE}/${BUILTIN_MODEL_REPO}/resolve/main/${BUILTIN_MODEL_TOKENIZER}"

  download_file "$MODEL_ONNX_URL" "$MODEL_ONNX_DEST"
  download_file "$MODEL_TOK_URL"  "$MODEL_TOK_DEST"

  # Verify files look real (not LFS pointers)
  ONNX_SIZE=$(wc -c < "$MODEL_ONNX_DEST" | tr -d ' ')
  if [[ "$ONNX_SIZE" -lt 1000000 ]]; then
    warn "model.onnx seems too small (${ONNX_SIZE} bytes) — may be an LFS pointer."
    warn "Try: HF_MIRROR=https://hf-mirror.com ./scripts/build.sh --onnx"
    # Attempt ?download=true suffix (works on HF CDN for LFS files)
    rm -f "$MODEL_ONNX_DEST"
    download_file "${MODEL_ONNX_URL}?download=true" "$MODEL_ONNX_DEST"
    ONNX_SIZE=$(wc -c < "$MODEL_ONNX_DEST" | tr -d ' ')
    if [[ "$ONNX_SIZE" -lt 1000000 ]]; then
      error "Failed to download real model.onnx. Set HF_MIRROR=https://hf-mirror.com and retry."
    fi
  fi

  info "model.onnx:      $(echo "scale=1; ${ONNX_SIZE}/1048576" | bc) MB"
  TOK_SIZE=$(wc -c < "$MODEL_TOK_DEST" | tr -d ' ')
  info "tokenizer.json:  $(echo "scale=0; ${TOK_SIZE}/1024" | bc) KB"

  export CGO_LDFLAGS="-L${LIBS_DIR}"
  info "CGO_LDFLAGS=${CGO_LDFLAGS}"
fi

# ── macOS deployment target (suppress linker version warning) ─────────────────
if [[ "$(go env GOOS)" == "darwin" ]]; then
  # Match the SDK version used by prebuilt libs to avoid:
  # "ld: warning: object file was built for newer 'macOS' version than being linked"
  export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-15.5}"
  # Also pass via CGO_LDFLAGS so Go's cgo linker picks it up
  export CGO_LDFLAGS="${CGO_LDFLAGS} -mmacosx-version-min=${MACOSX_DEPLOYMENT_TARGET}"
  info "MACOSX_DEPLOYMENT_TARGET=${MACOSX_DEPLOYMENT_TARGET}"
fi

# ── Step 2: Build axon ────────────────────────────────────────────────────────
step "Building axon (tags: ${BUILD_TAGS})"

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS="-s -w -X github.com/hsiaosiyuan0/axon/cmd.Version=${VERSION}"

set +e
BUILD_OUTPUT=$(CGO_ENABLED=1 \
CGO_CFLAGS="-DSQLITE_ENABLE_FTS5" \
go build \
  -tags "${BUILD_TAGS}" \
  -ldflags "${LDFLAGS}" \
  -o "${BINARY}" . 2>&1)
BUILD_EXIT=$?
set -e

# Filter known harmless linker warnings
FILTERED=$(echo "$BUILD_OUTPUT" | grep -v "ignoring duplicate libraries" || true)
if [[ -n "$FILTERED" ]]; then
  echo "$FILTERED"
fi

if [[ $BUILD_EXIT -ne 0 ]]; then
  exit $BUILD_EXIT
fi

info "Built: ./${BINARY} (version=${VERSION}, tags=${BUILD_TAGS})"

# ── Done ─────────────────────────────────────────────────────────────────────
echo
if $ONNX_BUILD; then
  echo "🎉 axon built with embedded ONNX Runtime + tokenizers + built-in model!"
  echo "   • libonnxruntime is bundled inside the binary — no extra setup needed."
  echo "   • Built-in model (bge-small-zh-v1.5) is embedded — works offline out of the box."
  echo "   • Download more models: axon model download bge-m3"
else
  echo "🎉 axon built (basic mode, no ONNX)"
  echo "   For ONNX + embedded model: $0 --onnx"
fi
