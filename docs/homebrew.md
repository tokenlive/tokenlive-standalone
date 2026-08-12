# Homebrew 安装（`brew services`）

单机 All-in-one：一个 `tokenlive` 进程提供 Gateway + Admin。

设计要点：

1. **编译期注入默认路径**（`ldflags -X main.DefaultConfigPath=...` 等）
2. **`service` 只启动二进制**，不带一长串参数
3. 用户用 Homebrew 标准方式启停：

```bash
brew services start tokenlive
brew services stop tokenlive
brew services restart tokenlive
```

## 本机安装（三仓源码）

```
Projects/
  tokenlive-gateway/
  tokenlive-admin/
  tokenlive-standalone/
```

```bash
cd tokenlive-standalone
./scripts/brew-install-local.sh
```

脚本会：

1. 打包二进制，并把 `$(brew --prefix)` 下的默认路径写入二进制
2. 安装到 Homebrew 前缀（`bin` / `etc` / `share` / `var`）
3. 注册 LaunchAgent，供 `brew services` 使用

### 启停与诊断

```bash
brew services start tokenlive
brew services stop tokenlive
brew services list | grep tokenlive
```

查看运行日志与报错：

```bash
tokenlive logs          # 查看最近日志输出
tokenlive logs -f       # 实时追踪日志 (tail -f)
tokenlive logs -e       # 只看错误日志
tokenlive logs --check  # 检测日志中是否有 ERROR / FATAL
tokenlive status        # 查看服务状态与日志文件位置
```

也可前台运行（无参数，路径已内置）：

```bash
tokenlive
```

### 路径

| 用途 | 路径 |
|------|------|
| 二进制 | `$(brew --prefix)/bin/tokenlive` |
| 活动配置 | `$(brew --prefix)/etc/tokenlive/config.yml` |
| 最新默认配置 | `$(brew --prefix)/etc/tokenlive/config.yml.default` |
| 数据 | `$(brew --prefix)/var/tokenlive` |
| Admin | `$(brew --prefix)/share/tokenlive/admin` |
| SPA | `$(brew --prefix)/share/tokenlive/web` |
| 服务日志 | `$(brew --prefix)/var/log/tokenlive.log` |

### 配置升级规则

- 如果活动配置与上一版默认配置完全一致，升级会自动安装新版默认配置。
- 如果活动配置有任何修改（包括注释和空白），升级会保留活动配置。
- 新版默认配置始终写入 `config.yml.default`，可手动比较：

```bash
diff "$(brew --prefix)/etc/tokenlive/config.yml" \
     "$(brew --prefix)/etc/tokenlive/config.yml.default"
```

首次使用该升级机制且缺少 `config.yml.default` 时，安装程序会保留现有活动配置，
只建立新的默认配置基准。

### 使用

- 打开：http://127.0.0.1:2525  
- 登录：`admin` / `admin`

### 卸载

```bash
./scripts/brew-uninstall-local.sh
# 连配置/数据一起删：
./scripts/brew-uninstall-local.sh --purge
```

## 仅打包

```bash
VERSION=0.2.0 BREW_PREFIX="$(brew --prefix)" ./scripts/package-release.sh
# dist/tokenlive-0.2.0/{bin,share,etc}
```

## 正式 tap / 自动发版

用户侧：

```bash
brew tap tokenlive/tokenlive
brew install tokenlive
# 升级：
brew update && brew upgrade tokenlive
brew services restart tokenlive
```

维护者：推 `vX.Y.Z` tag 到本仓即可触发 [`.github/workflows/release-brew.yml`](../.github/workflows/release-brew.yml)：

1. 在 `macos-14` 上构建 `tokenlive-X.Y.Z-darwin-arm64.tar.gz`（bake `/opt/homebrew` 默认路径）
2. 创建/更新 GitHub Release 并上传资产
3. 更新 `tokenlive/homebrew-tokenlive` Formula 的 `version` / `url` / `sha256`

本地等价命令：

```bash
VERSION=0.2.0 ./scripts/publish-brew-release.sh
```

一次性密钥（仓库 Secrets）：

| Secret | 用途 |
|--------|------|
| `HOMEBREW_TAP_TOKEN` | 对 `tokenlive/homebrew-tokenlive` 有 `contents:write` 的 PAT，用于推 Formula |

发版前请确认：

1. `tokenlive-gateway` / `tokenlive-admin` 已打好对应 module tag  
2. 本仓 `go.mod` 已 pin 到这些版本并提交  
3. 再打本仓 `vX.Y.Z` 并 `git push origin vX.Y.Z`
