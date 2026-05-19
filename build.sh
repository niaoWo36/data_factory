#!/usr/bin/env bash
# build.sh – 跨平台打包脚本 (macOS / Linux)
# 用法:
#   ./build.sh              # 打包当前平台
#   ./build.sh all          # 打包所有平台 (mac-arm64, mac-amd64, win-amd64, linux-amd64)
#   ./build.sh mac          # 仅打包 macOS (arm64 + amd64)
#   ./build.sh win          # 仅打包 Windows (amd64)
#   ./build.sh linux        # 仅打包 Linux (amd64)
#   ./build.sh clean        # 删除 dist/ 目录

set -euo pipefail

APP_NAME="data_factory"
MODULE=$(go list -m)
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS="-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}"
DIST_DIR="dist"
TARGET=${1:-current}

info()  { echo "  [BUILD] $*"; }
ok()    { echo "  ✅  $*"; }
clean() { rm -rf "${DIST_DIR}"; echo "  🗑️  dist/ 已清理"; }

build_target() {
    local GOOS=$1 GOARCH=$2 SUFFIX=$3 LABEL=$4
    local OUT_DIR="${DIST_DIR}/data-${LABEL}"

    # 删除旧的打包目录
    if [ -d "${OUT_DIR}" ]; then
        rm -rf "${OUT_DIR}"
        info "已清理旧目录: ${OUT_DIR}"
    fi
    mkdir -p "${OUT_DIR}"

    local BIN="${OUT_DIR}/${APP_NAME}${SUFFIX}"
    info "GOOS=${GOOS} GOARCH=${GOARCH} → ${BIN}"
    GOOS=${GOOS} GOARCH=${GOARCH} CGO_ENABLED=0 \
        go build -trimpath -ldflags "${LDFLAGS}" -o "${BIN}" .
    ok "${BIN} ($(du -sh "${BIN}" | cut -f1))"

    # 复制对应平台的启动脚本
    if [ "${GOOS}" = "windows" ]; then
        cp scripts/start.bat "${OUT_DIR}/start.bat"
        ok "启动脚本: ${OUT_DIR}/start.bat"
    else
        cp scripts/start.sh "${OUT_DIR}/start.sh"
        chmod +x "${OUT_DIR}/start.sh"
        ok "启动脚本: ${OUT_DIR}/start.sh"
    fi
}

build_mac()   {
    build_target darwin  arm64  ""     "macos-arm64"
    build_target darwin  amd64  ""     "macos-amd64"
}
build_win()   { build_target windows amd64  ".exe" "windows-amd64"; }
build_linux() { build_target linux   amd64  ""     "linux-amd64";   }

build_current() {
    local GOOS GOARCH
    GOOS=$(go env GOOS)
    GOARCH=$(go env GOARCH)
    local SUFFIX=""
    [[ "${GOOS}" == "windows" ]] && SUFFIX=".exe"
    build_target "${GOOS}" "${GOARCH}" "${SUFFIX}" "${GOOS}-${GOARCH}"
}

echo ""
echo "╔═══════════════════════════════════════════╗"
echo "║  data_factory 打包工具  v${VERSION}         "
echo "╚═══════════════════════════════════════════╝"
echo "  模块: ${MODULE}"
echo "  时间: ${BUILD_TIME}"
echo ""

mkdir -p "${DIST_DIR}"

case "${TARGET}" in
    all)     build_mac; build_win; build_linux ;;
    mac)     build_mac ;;
    win)     build_win ;;
    linux)   build_linux ;;
    clean)   clean; exit 0 ;;
    current) build_current ;;
    *)       echo "未知目标: ${TARGET}"; exit 1 ;;
esac

echo ""
echo "📦 输出目录: ${DIST_DIR}/"
ls -1 "${DIST_DIR}"/ 2>/dev/null | sed 's/^/    /'
echo ""
echo "✨ 打包完成"
