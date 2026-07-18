# tokenlive-standalone

[English](./README.md) | 中文版

> 单进程 All-in-one LLM API 网关 + 管理控制台。

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

## 两种部署形态

| 形态 | 仓库 / 制品 | 适用场景 |
|------|-------------|----------|
| **分部署（主线）** | [tokenlive-gateway](https://github.com/tokenlive/tokenlive-gateway) + [tokenlive-admin](https://github.com/tokenlive/tokenlive-admin) | 生产 / 多实例 |
| **All-in-one（本仓）** | `tokenlive` 二进制 | 单机 / Homebrew |

本仓**不**替代主线双进程部署。Gateway + Admin 必须同时启用。

## 安装

### Homebrew（macOS）

```bash
brew tap tokenlive/tokenlive
brew install tokenlive
brew services start tokenlive
# http://127.0.0.1:2525  —  admin / admin
```

停止：`brew services stop tokenlive`

卸载：

```bash
brew uninstall tokenlive
brew untap tokenlive/tokenlive
```

详见 [docs/homebrew.md](docs/homebrew.md)

### 从源码构建

前置：同级目录 checkout 三个仓库。

```
Projects/
  tokenlive-gateway/
  tokenlive-admin/
  tokenlive-standalone/   # 本仓
```

```bash
go mod tidy
make run          # http://127.0.0.1:2525
make smoke        # 短暂启动并检查 health
```

构建管理控制台前端：

```bash
cd ../tokenlive-admin/frontend && npm ci && npm run build:prod
cd ../../tokenlive-standalone && make run
```

### 预编译 Release

从 [GitHub Releases](https://github.com/tokenlive/tokenlive-standalone/releases) 下载。

## 架构

```
tokenlive (本仓)
  ├─ adminapp (tokenlive-admin)       → /api/v1 + SPA
  ├─ confighub                        → Embedded GatewayProvider
  └─ pkg/gateway (tokenlive-gateway)  → /v1/* Engine
```

Admin 写库 → `OnConfigChanged` → ConfigHub 刷新 → `ApplyGatewayConfig` / 清缓存。

## 配置

| 参数 | 说明 |
|------|------|
| `-conf` | Gateway YAML（须设 `gateway.config_source: embedded`） |
| `-data-dir` | 数据目录（默认 `data`） |
| `-admin-workdir` | Admin TOML 目录（默认捆绑 `configs/admin`） |
| `-admin-config` | admin 配置子集；捆绑配置可省略 |
| `-admin-static` | SPA 目录，可选 |

示例 YAML：`config/all-in-one.example.yml`
捆绑 admin 配置：`configs/admin/`

默认端口 **2525**，默认数据库 SQLite（`data/tokenlive.db`），默认管理员 `admin` / `admin`（已关验证码）。

## 开发

```bash
make test
make run
make smoke
```

发布打包：

```bash
VERSION=0.1.0 ./scripts/package-release.sh
```

本地 Homebrew 安装（源码构建）：

```bash
./scripts/brew-install-local.sh
```

## 状态

- [x] 脚手架 + OpenSpec 契约
- [x] Gateway / Admin embed API
- [x] ConfigHub + 热更新桥接
- [x] 主线 tag 钉扎 + 正式 Homebrew tap
- [ ] 完整 E2E（登录控制台 → 配模型 → chat completions）

## 许可

Apache 2.0
