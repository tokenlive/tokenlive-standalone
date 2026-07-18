# Homebrew / 本机安装（All-in-one）

单机形态：一个 `tokenlive` 进程 = Gateway + Admin，默认端口 **2525**。

> 生产多实例仍推荐分部署：`tokenlive-gateway` + `tokenlive-admin`。

## 本机安装（当前推荐）

仓库布局：

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

会：

1. 构建前端 + Go 二进制（`scripts/package-release.sh`）
2. 安装到 `$(brew --prefix)/bin`、`share/tokenlive`、`etc/tokenlive`
3. 写入 LaunchAgent：`~/Library/LaunchAgents/homebrew.mxcl.tokenlive.plist`

### 启动 / 停止

```bash
tokenlive-start
tokenlive-stop
```

或：

```bash
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/homebrew.mxcl.tokenlive.plist
launchctl bootout "gui/$(id -u)/homebrew.mxcl.tokenlive"
```

### 路径

| 用途 | 路径 |
|------|------|
| 二进制 | `$(brew --prefix)/bin/tokenlive` |
| 配置 | `$(brew --prefix)/etc/tokenlive/config.yml` |
| 数据 | `$(brew --prefix)/var/tokenlive` |
| Admin 配置 | `$(brew --prefix)/share/tokenlive/admin` |
| 控制台 SPA | `$(brew --prefix)/share/tokenlive/web` |
| 日志 | `$(brew --prefix)/var/log/tokenlive.log` |

### 使用

- 打开：http://127.0.0.1:2525  
- 登录：`admin` / `admin`  
- 健康检查：`curl -s http://127.0.0.1:2525/health`

### 卸载

```bash
./scripts/brew-uninstall-local.sh
# 数据/配置默认保留；可手动删：
# rm -rf $(brew --prefix)/var/tokenlive $(brew --prefix)/etc/tokenlive
```

## 仅打包

```bash
VERSION=0.1.0 ./scripts/package-release.sh
# dist/tokenlive-0.1.0/{bin,share,etc}
```

## Formula 草案

`packaging/homebrew/tokenlive.rb` 供将来正式 tap 使用。  
在 **gateway/admin 发布 module tag** 并去掉 `replace` 后，可改为：

```bash
brew tap tokenlive/tokenlive
brew install tokenlive
brew services start tokenlive
```

当前开发期以 `brew-install-local.sh` 为准。
