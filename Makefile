APP_NAME  := data_factory
DIST_DIR  := dist
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS   := -s -w -X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME)

.PHONY: all mac win linux current clean help

## all: 打包所有平台 (macOS arm64/amd64, Windows amd64, Linux amd64)
all: mac win linux

## mac: 打包 macOS (Apple Silicon + Intel)
mac:
	@mkdir -p $(DIST_DIR)/$(APP_NAME)_$(VERSION)_macos-arm64
	@echo "  [BUILD] darwin/arm64"
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(DIST_DIR)/$(APP_NAME)_$(VERSION)_macos-arm64/$(APP_NAME) .
	@echo "  ✅  $(DIST_DIR)/$(APP_NAME)_$(VERSION)_macos-arm64/$(APP_NAME)"

	@mkdir -p $(DIST_DIR)/$(APP_NAME)_$(VERSION)_macos-amd64
	@echo "  [BUILD] darwin/amd64"
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(DIST_DIR)/$(APP_NAME)_$(VERSION)_macos-amd64/$(APP_NAME) .
	@echo "  ✅  $(DIST_DIR)/$(APP_NAME)_$(VERSION)_macos-amd64/$(APP_NAME)"

## win: 打包 Windows amd64
win:
	@mkdir -p $(DIST_DIR)/$(APP_NAME)_$(VERSION)_windows-amd64
	@echo "  [BUILD] windows/amd64"
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(DIST_DIR)/$(APP_NAME)_$(VERSION)_windows-amd64/$(APP_NAME).exe .
	@echo "  ✅  $(DIST_DIR)/$(APP_NAME)_$(VERSION)_windows-amd64/$(APP_NAME).exe"

## linux: 打包 Linux amd64
linux:
	@mkdir -p $(DIST_DIR)/$(APP_NAME)_$(VERSION)_linux-amd64
	@echo "  [BUILD] linux/amd64"
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" \
		-o $(DIST_DIR)/$(APP_NAME)_$(VERSION)_linux-amd64/$(APP_NAME) .
	@echo "  ✅  $(DIST_DIR)/$(APP_NAME)_$(VERSION)_linux-amd64/$(APP_NAME)"

## current: 仅打包当前运行平台
current:
	@mkdir -p $(DIST_DIR)
	@echo "  [BUILD] $(shell go env GOOS)/$(shell go env GOARCH)"
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(APP_NAME) .
	@echo "  ✅  $(DIST_DIR)/$(APP_NAME)"

## clean: 删除 dist/ 目录
clean:
	rm -rf $(DIST_DIR)
	@echo "  🗑️  dist/ 已清理"

## help: 显示帮助
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
