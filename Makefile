# sesscope —— 跨 Agent（opencode / dsh / codex / claude）会话 → 导出 JSON → DSH 轨迹风格耗时 HTML

BINARY := out/sesscope
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || date +%Y%m%d-%H%M%S)
GO ?= go
SESSION ?=

.PHONY: all build fmt vet test lint verify golden demo list export sources doctor audit smoke clean

all: build

## build: 编译单文件二进制（内嵌 UI 资源）
build:
	$(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) .

## fmt: 格式检查（有未格式化文件就失败）
fmt:
	@out=$$(gofmt -l . | grep -v '^out/'); if [ -n "$$out" ]; then echo "需要 gofmt："; echo "$$out"; exit 1; fi

## vet: 静态检查
vet:
	$(GO) vet ./...

## test: 单元测试 + 黄金用例
test:
	$(GO) test ./...

## verify: 全套检查（格式 + 静态 + 测试）
verify: fmt vet test

## audit: 字段门禁：检查抽象会话里该有却没有的字段（需本机有会话；有缺口时非 0 退出）
audit: build
	$(BINARY) audit -n $${AUDIT_LIMIT:-10}

## lint: 可选，装了 staticcheck 才跑
lint:
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "跳过：没有 staticcheck"

## golden: 用真实导出重写黄金用例（只在确认改动符合预期时用）
golden:
	$(GO) test ./internal/trace -run TestGoldenParity -update

## sources: 列出本机所有来源与存储状态（附发现规则）
sources: build
	$(BINARY) sources --explain

## doctor: 逐存储体检（含未注册记录类型）
doctor: build
	$(BINARY) doctor --drift

## smoke: 真机四源端到端（sources → 每源导出 1 个会话 → 页面冒烟）
smoke: build
	bash tools/smoke-sources.sh

## list: 列出本机工作区与会话
list: build
	$(BINARY) workspaces
	@echo
	$(BINARY) sessions -limit 10

## export: 导出并渲染某个会话（make export SESSION=ses_xxx）
export: build
	@test -n "$(SESSION)" || (echo "用法：make export SESSION=ses_xxx（先用 make list 看 id）"; exit 2)
	$(BINARY) export -session "$(SESSION)" -out out/demo/session-export.json -html out/demo/trace.html

## demo: 用本机最近活跃的会话走一遍完整链路
demo: build
	@set -e; id=$$($(BINARY) sessions -limit 1 -json 2>/dev/null | sed -n 's/.*"id": "\(ses_[^"]*\)".*/\1/p' | head -1); \
	  echo "会话：$$id"; $(BINARY) export -session "$$id" -out out/demo/session-export.json -html out/demo/trace.html

clean:
	rm -rf out
