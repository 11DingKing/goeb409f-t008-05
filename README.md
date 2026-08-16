# 额济纳旗源网荷储微电网 · 黑启动与离并网切换协同系统

一套自包含的 Go 后端，为额济纳旗源网荷储微电网在单回路主网失电场景下提供
**黑启动 → 离网独立运行 → 自动同期并网** 的指挥协同机制，覆盖状态流编排、
并发优先级、幂等推进与失败恢复。

## 业务能力

- 主网失电后，调度值长下达黑启动令；构网型储能电池舱作为**唯一黑启动电源**，
  依次带起风电汇集站、光伏场站无功支撑，逐步恢复辖区母线负荷，进入离网运行。
- 并网切换时，储能控制员、风电场站值班员、光伏场站值班员三方须按自动同期条件
  （相角差 / 频率差 / 电压差）确认通过，调度值长方可下令合闸并网。
- 离网运行期间，若风电出力与负荷预测偏差超阈值，储能控制员须在 10 分钟内启动
  备用柴油机组平抑波动（由后台监控看门狗检测超时）。
- **并发优先级**：黑启动演练与真实离网切换同时触发时，真实切换优先，演练指令
  挂起并记录。
- **失败恢复**：合闸并网连续两次失败时，退回离网运行并重新执行黑启动序列，
  同时通知旗县应急值班室，确保辖区供电不中断。

## 技术栈

- Go 1.26，仅使用标准库（`net/http`、`log/slog`、`encoding/json` 等），无外部依赖。
- 分层架构：领域状态机 / 应用编排 / 持久化 / HTTP 接入 / 后台任务 / 配置。

## 目录结构

```
cmd/microgrid/main.go          程序入口，装配各层并启停
internal/domain/               聚合根与状态机（process / synchronization / events）
internal/application/          应用服务：命令编排、并发边界、幂等、截止期检测
internal/store/                内存仓储（线程安全、深拷贝隔离）
internal/httpapi/              REST 接入与错误映射
internal/background/           后台监控看门狗（柴油超时检测）
internal/config/               配置加载（文件 + 环境变量 + 默认值）
config.json                    项目配置
Dockerfile                     多阶段、多架构构建
```

## 配置

默认读取当前目录 `config.json`，可用环境变量 `MICROGRID_CONFIG` 指定路径；文件
缺失时使用内置默认值。可用环境变量覆盖：

| 变量 | 说明 | 默认 |
| --- | --- | --- |
| `MICROGRID_CONFIG` | 配置文件路径 | `config.json` |
| `MICROGRID_HTTP_ADDR` | 监听地址 | `:51326` |
| `MICROGRID_MONITOR_INTERVAL` | 后台监控周期 | `5s` |

`config.json` 中的 `settings` 定义同期容差、偏差阈值、黑启动窗口（5m）、柴油
启动窗口（10m）、最大合闸失败次数（2）等业务参数。

## 本地运行

```bash
go run ./cmd/microgrid
# 监听 :51326
```

## 主要接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/healthz` | 健康检查 |
| `POST` | `/api/v1/processes` | 下达黑启动令（`{"kind":"real\|drill","grid_lost_at":"RFC3339"}`） |
| `GET` | `/api/v1/processes` | 列出全部过程 |
| `GET` | `/api/v1/processes/{id}` | 查询过程详情与事件流 |
| `POST` | `/api/v1/processes/{id}/storage` | 储能电池舱建压上线 |
| `POST` | `/api/v1/processes/{id}/wind` | 风电汇集站无功支撑就绪 |
| `POST` | `/api/v1/processes/{id}/pv` | 光伏场站无功支撑就绪 |
| `POST` | `/api/v1/processes/{id}/loads` | 恢复辖区母线负荷，进入离网运行 |
| `POST` | `/api/v1/processes/{id}/synchronize` | 进入自动同期检查阶段 |
| `POST` | `/api/v1/processes/{id}/confirm` | 三方之一提交同期确认 |
| `POST` | `/api/v1/processes/{id}/breaker` | 合闸并网（`{"success":bool}`） |
| `POST` | `/api/v1/processes/{id}/deviation` | 上报风电/负荷偏差 |
| `POST` | `/api/v1/processes/{id}/diesel` | 启动备用柴油机组 |

错误映射：未找到 → `404`；非法状态迁移 → `409`；参数校验失败 → `400`。

## 测试

```bash
go test -timeout=120s -count=1 ./...
```

测试覆盖正常路径、错误路径、状态迁移、并发序列化、后台取消与失败恢复。

## Docker

```bash
# 构建
docker build -t ejina-microgrid .

# 运行（映射 51326 端口）
docker run --rm -p 51326:51326 ejina-microgrid

# 多架构构建（amd64 / arm64）
docker buildx build --platform linux/amd64,linux/arm64 -t ejina-microgrid:latest .
```

最终镜像仅包含运行文件与配置，`EXPOSE 51326`。
