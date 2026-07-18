# tokenlive-standalone

TokenLive **All-in-one** 组装仓：单进程同时运行 Gateway（LLM 代理）与 Admin（控制台）。

## 部署形态

| 形态 | 仓库 / 制品 | 说明 |
|------|-------------|------|
| **分部署（主线）** | [`tokenlive-gateway`](https://github.com/tokenlive/tokenlive-gateway) + [`tokenlive-admin`](https://github.com/tokenlive/tokenlive-admin) | 生产 / 多实例推荐 |
| **All-in-one（本仓）** | `tokenlive-standalone` → 二进制 `tokenlive` | 单机 / Homebrew；Gateway + Admin 必须同时启用 |

本仓 **不** 替代主线双进程部署。

## 状态

脚手架 + OpenSpec 设计已迁入。实现见：

`openspec/changes/merge-gateway-admin/`

## 本地开发（规划）

```bash
# 依赖主线库 tag 或本地 replace
go mod tidy
go run ./cmd/tokenlive -conf config/all-in-one.example.yml
```

## OpenSpec

跨仓契约与任务列表：

- `openspec/changes/merge-gateway-admin/proposal.md`
- `openspec/changes/merge-gateway-admin/design.md`
- `openspec/changes/merge-gateway-admin/tasks.md`
- `openspec/changes/merge-gateway-admin/specs/`
