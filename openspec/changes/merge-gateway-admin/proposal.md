## Why

TokenLive 生产主线是 **Gateway 与 Admin 分进程独立部署**。单机用户希望 `brew install` 即可同时使用控制台与代理。为避免污染两个主线仓库、并保持分部署零破坏，本变更以 **新开组装仓 `tokenlive-standalone`** 实现 All-in-one，仅在该仓做进程合并与发行，不把 Admin 源码迁入 Gateway 仓。

## What Changes

系统 **仅有两种部署形态**：

| 形态 | 仓库 / 进程 | 说明 |
|------|-------------|------|
| **分部署（主线）** | `tokenlive-gateway` + `tokenlive-admin` 各自独立进程 | 现有架构；`config_source: local\|redis\|http`；**本变更不改主线行为** |
| **All-in-one（附加）** | 新仓 **`tokenlive-standalone`** 单一进程 = Gateway + Admin | `config_source: embedded`；无「只开 Gateway、关 Admin」 |

具体变更：

- **新建仓库 `tokenlive-standalone`**（组装仓）：
  - 通过 Go module 依赖（钉 tag）引入 `tokenlive-gateway` 与 `tokenlive-admin` 的可嵌入库入口。
  - 实现合并层：单 Gin、统一生命周期、`ConfigHub` / `embedded`、all-in-one 配置模板、Homebrew Formula。
  - 二进制名建议 `tokenlive`（或文档约定名）。
- **主线两仓小幅适配（library 化，非业务合并）**：
  - Gateway：导出可被外部 `main` 调用的 Engine / LLM 路由注册 API（若尚缺）。
  - Admin：导出可挂到外部 `*gin.Engine` 的模块 Init / 路由注册 API（若尚缺）。
  - **不**把 Admin 业务代码 copy 进 Gateway；**不**把分部署默认改成 all-in-one。
- **Homebrew**：只包装 `tokenlive-standalone`。
- **非目标**：不 monorepo 合并三仓；不删除独立 Admin/Gateway 仓；不重写 Engine。
- **BREAKING**：无（主线路径无破坏）。All-in-one 为新仓新能力。

## Capabilities

### New Capabilities

- `all-in-one-runtime`：`tokenlive-standalone` 单进程同时运行完整 Gateway + 完整 Admin。
- `embedded-config-provider`：组装仓内进程内配置供给；分部署继续 `local|redis|http`。
- `homebrew-single-host`：基于 `tokenlive-standalone` 的 brew 安装与默认数据路径。
- `library-embed-surface`：Gateway / Admin 对外可嵌入的库边界（供 standalone 组装）。

### Modified Capabilities

- （无既有 `openspec/specs/` 基线；本变更以 New Capabilities 建立契约。）

## Impact

- **新仓**：`github.com/tokenlive/tokenlive-standalone`（名称以实际创建为准）承载 all-in-one 与 brew。
- **tokenlive-gateway / tokenlive-admin**：仅增加稳定 library API 与版本 tag；分部署 cmd 入口保持。
- **依赖方向**：`standalone → gateway`、`standalone → admin`；主线两仓 **互不依赖**、**不依赖** standalone。
- **版本**：standalone 的 `go.mod` 钉死两仓版本；升级 = 升依赖。
- **文档**：主线仍写分部署；standalone README + brew 为附加形态。
