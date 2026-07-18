# tokenlive-standalone

TokenLive **All-in-one** 组装仓：单进程同时运行 Gateway（LLM 代理）与 Admin（控制台）。

## 两种部署形态

| 形态 | 仓库 / 制品 | 说明 |
|------|-------------|------|
| **分部署（主线）** | [tokenlive-gateway](https://github.com/tokenlive/tokenlive-gateway) + [tokenlive-admin](https://github.com/tokenlive/tokenlive-admin) | 生产 / 多实例推荐 |
| **All-in-one（本仓）** | `tokenlive` 二进制 | 单机 / Homebrew；**Gateway + Admin 必须同时启用** |

本仓 **不** 替代主线双进程部署。

## 架构（简）

```
tokenlive (本仓)
  ├─ adminapp (tokenlive-admin)  → /api/v1 + SPA
  ├─ confighub                   → Embedded GatewayProvider
  └─ pkg/gateway (tokenlive-gateway) → /v1/* Engine
```

Admin 写库 → `OnConfigChanged` → ConfigHub 刷新 → `ApplyGatewayConfig` / 清缓存。

## 本地开发

前置：与本仓同级 checkout：

```
Projects/
  tokenlive-gateway/
  tokenlive-admin/
  tokenlive-standalone/   # 本仓
```

`go.mod` 使用 `replace` 指向本地两库（发版前改为 tag）。

```bash
go mod tidy
make test
make run          # 或 ./scripts/dev-run.sh
curl -s http://127.0.0.1:2525/health
make smoke        # 短暂启动并检查 health
```

默认端口 **2525**。启动时强制 `DB_TYPE=sqlite3`，库文件 `data/tokenlive.db`（`-data-dir` 可改）。

默认管理员：`admin` / `admin`（单机配置已 **禁用验证码**）。

可选前端：

```bash
cd ../tokenlive-admin/frontend && npm ci && npm run build
cd ../../tokenlive-standalone
make run ADMIN_STATIC=../tokenlive-admin/frontend/dist
```

### 配置

| 参数 | 含义 |
|------|------|
| `-conf` | Gateway YAML（须 `gateway.config_source: embedded`） |
| `-data-dir` | 数据目录（默认 `data`） |
| `-admin-workdir` | Admin TOML 目录（默认本仓 `configs/admin`） |
| `-admin-config` | workdir 下子集；bundled 配置可省略 |
| `-admin-static` | SPA 目录，可选 |

示例 YAML：`config/all-in-one.example.yml`。  
Admin 捆绑配置：`configs/admin/`（不依赖环境里的 `DB_TYPE=mysql`）。

## OpenSpec

跨仓契约与任务：`openspec/changes/merge-gateway-admin/`

- `proposal.md` / `design.md` / `tasks.md`
- `embed-api.md` — 库边界签名
- `specs/*`

## 状态

- [x] 脚手架 + OpenSpec
- [x] Gateway / Admin embed API（本地未发 tag 时用 replace）
- [x] ConfigHub + 热更新桥接
- [ ] 主线 tag 钉扎（去掉 replace）
- [ ] Homebrew Formula
- [ ] 完整 E2E（登录控制台 → 配模型 → chat completions）

## 许可

与 TokenLive 主线项目一致。
