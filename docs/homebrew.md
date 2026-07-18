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

### 启停

```bash
brew services start tokenlive
brew services stop tokenlive
brew services list | grep tokenlive
```

也可前台运行（无参数，路径已内置）：

```bash
tokenlive
```

### 路径

| 用途 | 路径 |
|------|------|
| 二进制 | `$(brew --prefix)/bin/tokenlive` |
| 配置 | `$(brew --prefix)/etc/tokenlive/config.yml` |
| 数据 | `$(brew --prefix)/var/tokenlive` |
| Admin | `$(brew --prefix)/share/tokenlive/admin` |
| SPA | `$(brew --prefix)/share/tokenlive/web` |
| 服务日志 | `$(brew --prefix)/var/log/tokenlive.log` |

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
VERSION=0.1.0 BREW_PREFIX="$(brew --prefix)" ./scripts/package-release.sh
# dist/tokenlive-0.1.0/{bin,share,etc}
```

## 正式 tap（后续）

gateway/admin 发 module tag、去掉 `replace` 后：

1. 发布带前端的 release tarball  
2. Formula 使用 `url` + `sha256`  
3. `brew tap tokenlive/tokenlive && brew install tokenlive`
