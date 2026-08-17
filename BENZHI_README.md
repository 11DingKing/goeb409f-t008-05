# BENZHI_README

## 项目说明

- 项目：11DingKing/goeb409f-t008-05
- 项目用途：一套自包含的 Go 后端，为额济纳旗源网荷储微电网在单回路主网失电场景下提供 黑启动 → 离网独立运行 → 自动同期并网 的指挥协同机制，覆盖状态流编排、 并发优先级、幂等推进与失败恢复。
- Go 工具链：`golang:1.26`
- 前端工具链：无

## 标准构建、运行和测试命令

进入容器后执行：

```bash
# 编译
cd '/app' && GOTOOLCHAIN=local go build ./...

# 启动
cd '/app' && GOTOOLCHAIN=local go run ./cmd/microgrid

# 测试
cd '/app' && GOTOOLCHAIN=local go test ./...
```

## Docker 构建和进入容器

```bash
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-task-30-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-task-30-arm64 linux/arm64
docker run -it benzhi-task-30-amd64:latest
docker run -it --platform linux/arm64 benzhi-task-30-arm64:latest
```

## 题目验证命令

1. 预期退出码 1：`go test ./internal/domain/ -run "^TestProcess_RestartStartsCleanDeviationCycle$" -count=1 -v`

## Bug 复现

Bug 现象、触发步骤和完整错误信息见 `BUG_REPRO.md`。
