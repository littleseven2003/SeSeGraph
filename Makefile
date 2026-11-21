# SeSeGraph 构建入口
SHELL := /bin/sh

BINARY  ?= bin/sesegraph
VERSION ?= dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: all frontend build test typecheck clean cross

all: build

# 构建前端并把产物放入后端嵌入目录
frontend:
	cd frontend && pnpm install --frozen-lockfile && pnpm build
	rm -rf backend/internal/web/assets/static backend/internal/web/assets/index.html
	cp -R frontend/dist/. backend/internal/web/assets/

# 生成内嵌前端的单一可执行文件
build: frontend
	cd backend && CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o ../$(BINARY) ./cmd/server

test:
	cd backend && go test ./...

typecheck:
	cd frontend && pnpm typecheck

cross:
	cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/sesegraph-linux-amd64 ./cmd/server
	cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/sesegraph-linux-arm64 ./cmd/server
	cd backend && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/sesegraph-darwin-arm64 ./cmd/server
	cd backend && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/sesegraph-windows-amd64.exe ./cmd/server

clean:
	rm -rf bin frontend/dist backend/internal/web/assets/static backend/internal/web/assets/index.html
