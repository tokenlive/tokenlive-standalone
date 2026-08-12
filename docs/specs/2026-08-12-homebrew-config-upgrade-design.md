# Homebrew 配置安全升级设计

## 背景

TokenLive 的 Homebrew 安装将活动配置放在
`$(brew --prefix)/etc/tokenlive/config.yml`。该文件允许用户修改，因此升级不能
无条件覆盖；但如果始终保留旧文件，新版本新增的默认配置又无法自动生效。

本设计通过保存上一版官方默认配置，判断活动配置是否被用户修改：

- 用户未修改：自动更新为新版默认配置。
- 用户修改过：保留活动配置，并提供新版默认配置供比较和合并。

## 目标

- 保留用户对 `config.yml` 的任何修改。
- 未修改的安装自动采用新版完整默认配置。
- 判断过程确定、可测试，不依赖 YAML 字段语义。
- Homebrew Formula 与本地 Homebrew 风格安装脚本行为一致。
- 升级失败时不留下空文件或部分写入的配置。

## 非目标

- 不自动合并用户配置与新版默认配置。
- 不判断某个 YAML 字段是用户修改还是旧版本默认值。
- 不忽略注释、空白或字段顺序变化；任何字节变化都视为用户修改。

## 文件约定

安装目录使用两个文件：

| 文件 | 用途 |
|------|------|
| `etc/tokenlive/config.yml` | TokenLive 实际读取的用户配置 |
| `etc/tokenlive/config.yml.default` | 当前安装版本的官方默认配置，也是下一次升级的比较基准 |

发布包内的 `stage/etc/tokenlive/config.yml` 是待安装的新版本默认配置。

## 安装与升级流程

### 全新安装

1. 将发布包中的默认配置安装为 `config.yml`。
2. 将同一内容安装为 `config.yml.default`。
3. 两个文件权限均设为 `0644`。

### 已安装且存在比较基准

升级开始时，先读取旧的 `config.yml.default`，再处理新版默认配置：

1. 对 `config.yml` 和旧的 `config.yml.default` 做字节级比较。
2. 如果完全一致，认定用户没有修改：
   - 记录比较结果。
   - 原子替换 `config.yml.default` 为新版默认配置。
   - 再原子替换 `config.yml` 为新版默认配置。
   - 输出“活动配置已自动升级”的提示。
3. 如果不一致，认定用户修改过：
   - 不改动 `config.yml`。
   - 原子替换 `config.yml.default` 为新版默认配置。
   - 输出“保留用户配置”的提示。

必须在更新 `config.yml.default` 之前完成比较，否则会错误地把版本差异判断为
用户修改。

### 旧安装没有比较基准

首次引入本机制时，旧安装可能只有 `config.yml`：

1. 保留现有 `config.yml`，不推测它是否被修改。
2. 将新版默认配置安装为 `config.yml.default`。
3. 提示用户这是首次建立比较基准，活动配置未被覆盖。

该策略宁可少做一次自动更新，也不能误覆盖用户配置。用户手动同步
`config.yml` 与 `config.yml.default` 后，后续升级即可进入自动更新路径。

### 活动配置缺失

如果 `config.yml` 不存在，无论比较基准是否存在，都安装新版默认配置为
`config.yml`，随后更新 `config.yml.default`。

## 原子写入与失败处理

- 新内容先写入目标目录中的临时文件。
- 设置权限并确认写入成功后，再通过同文件系统重命名替换目标文件。
- 自动更新时先替换 `.default`，最后才替换活动配置。活动配置替换之后没有其他
  可能导致升级失败的配置步骤。
- `.default` 更新失败时，不修改活动配置。
- `.default` 更新成功但活动配置更新失败时：
  - 旧活动配置保持完整。
  - 新 `.default` 已经落盘，下一次运行会安全地把活动配置视为已修改。
  - 安装或升级命令返回失败，并提示用户检查两个文件。
- 任何失败都输出具体失败文件和原因。
- 判断用户修改时使用文件内容比较，不使用修改时间。

## 用户提示

配置被保留时，安装输出应明确给出：

```text
TokenLive config was preserved because it differs from the previous default.
Active config:  <prefix>/etc/tokenlive/config.yml
New defaults:   <prefix>/etc/tokenlive/config.yml.default
Compare with:
  diff <prefix>/etc/tokenlive/config.yml <prefix>/etc/tokenlive/config.yml.default
```

配置被自动更新或首次建立比较基准时，也应输出对应状态，避免静默改变。

## 代码改动范围

- `scripts/install-brew-config.sh`
  - 新增共享配置安装助手，接收新版默认文件和目标配置目录。
  - 集中实现比较、临时文件、原子替换和状态输出。
- `packaging/homebrew/tokenlive.rb`
  - Formula 安装和升级时调用共享配置安装助手。
  - 安装并更新 `config.yml.default`。
  - 在 caveats 中说明两个配置文件的用途。
- `scripts/brew-install-local.sh`
  - 调用同一个共享配置安装助手。
  - 移除当前基于特定字段（例如 `events:`）判断是否覆盖的逻辑。
- `scripts/package-release.sh`
  - 继续生成新版默认配置工件，必要时明确默认配置文件命名。
- `docs/homebrew.md`
  - 记录升级行为、配置路径和手动比较方式。

TokenLive 运行时仍只读取 `config.yml`，无需改变配置加载优先级。

## 测试矩阵

至少覆盖以下场景：

| 场景 | 预期结果 |
|------|----------|
| 全新安装 | `config.yml` 和 `.default` 均为新版且内容一致 |
| 未修改配置后升级 | 两个文件均自动更新为新版 |
| 修改 YAML 值后升级 | 保留活动配置，`.default` 更新 |
| 只修改注释后升级 | 保留活动配置，`.default` 更新 |
| 旧安装缺少 `.default` | 保留活动配置并建立新版基准 |
| 活动配置缺失 | 恢复新版活动配置并更新基准 |
| 临时文件写入失败 | 原活动配置保持不变，升级报错 |
| 连续升级两次 | 第二次仍能正确识别是否发生用户修改 |

主要行为测试直接覆盖共享配置安装助手；Formula 和本地安装脚本另做薄层集成
测试，确认参数与路径传递正确。
