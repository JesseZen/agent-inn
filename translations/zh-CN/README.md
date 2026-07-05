**English** | [中文](./README.md)

Codex App 的本地代理管理器。单个二进制文件即可启动管理器 + 工作进程 + TUI。

## 架构

```
Codex App / CLI
      │
      ▼
┌──────────┐
│  Worker  │  ← Listens on a local port, forwards requests to upstream
│  (proxy) │  ← Filters image_generation, Chat Completions translation, etc.
└──────────┘
      │
      ▼
┌──────────┐
│ Upstream │  ← Upstream API service (OpenAI, OpenRouter, Groq, etc.)
└──────────┘

┌──────────┐
│ Manager  │  ← Manages Worker lifecycle, exposes HTTP API + SSE event stream
│          │  ← TUI communicates with Manager via API
└──────────┘
      │
      ▼
┌──────────┐
│   TUI    │  ← OpenTUI (SolidJS) terminal interface
│(OpenTUI) │  ← Conversational interaction, type / to trigger commands
└──────────┘
```

### 核心概念

| 概念 | 描述 |
|---------|-------------|
| <strong>管理器</strong> | 中央管理器 — 启动/停止工作进程，提供 HTTP API，TUI 连接至此 |
| <strong>工作进程</strong> | 监听端口的本地代理进程，将请求转发至指定上游 |
| <strong>上游</strong> | 上游 API 服务配置（base_url、api_key、api_format） |
| <strong>模块</strong> | 工作进程功能模块（请参阅下方的[模块](#构建与运行)） |

每个工作进程绑定一个上游。你可以同时在不同端口上运行多个指向不同上游的工作进程。

### 模块

| 模块 | 描述 |
|--------|-------------|
| `config_patch` | 自动修改 `~/.codex/config.toml` 以将 Codex 指向工作进程 |
| `image_filter` | 过滤 `image_generation` 工具调用 |
| `api_translate` | 聊天补全 ↔ 响应 API 翻译 |
| `model_override` | 通过 `params.model` 覆盖请求中的 `model` 字段 |
| `request_log` | 将请求方法和路径记录到 stderr |
| `debug_sse` | 将 SSE 块统计信息记录到 stderr |

## 构建与运行

### 前置条件

- Go 1.26+
- Bun 1.2+（用于 TUI）

### 构建

```bash

# 安装 TUI 依赖项
bun install

# 构建 Go 二进制文件
go build -o ainn .

```

### 配置

```bash
mkdir -p ${HOME}/.ainn

cp config.example.yaml ${HOME}/.ainn/config.yaml
# 编辑 ${HOME}/.ainn/config.yaml 以设置 worker 和上游服务
```

### 运行

```bash
./ainn
```

这个单一命令将启动管理器 → 启动所有工作进程 → 启动 TUI。

### 开发模式（前后端分离）

```bash
# 终端1：仅后端（默认管理端口为9090）
./ainn --config-dir ${HOME}/.ainn --manager-port 9090 &

# 终端2：带热重载的TUI
bun install  # 从项目根目录安装依赖（首次使用时需要）
cd tui && AINN_URL=http://localhost:9090 bun run dev
```

## TUI 操作

启动后，你将看到一个带有底部输入栏的空白屏幕。输入 `/` 即可打开带有模糊搜索功能的命令选择器。

### 命令列表

| 命令 | 别名 | 描述 |
|---------|-------|-------------|
| `/help` | | 显示所有命令 |
| `/settings` | `/config` | 编辑运行时设置并查看配置保存状态 |
| `/workers` | | 管理工作进程（创建、检查、编辑字段/模块、查看日志、重启/停止） |
| `/upstream` | | 管理上游（创建、编辑 base_url/api_key/api_format） |
| `/logs` | | 查看工作进程日志 |
| `/launch` | | 通过 cli-role 工作进程启动 Codex CLI |
| `/exit` | `/quit` `/q` | 退出 |

### 键盘快捷键

| 按键 | 操作 |
|-----|--------|
| `Ctrl+C` | 清除输入；按两次退出 |
| `Shift+Enter` | 输入中换行 |
| `↑` `↓` | 列表导航 |
| `Enter` | 确认选择 |
| `Esc` | 取消/返回 |

## 配置文件格式

```yaml
# Runtime settings
settings:
  state_dir: ~/.ainn
  log_dir: ~/.ainn/logs
  launch:
    default_mode: hosted-terminal
  terminal:
    host: tmux
    opener: default
    tmux:
      socket_name: ainn
      host_session: ainn-host
      host_start_mode: new-window

# Worker definitions
workers:
  codex-app:              # Worker name
    port: 6767            # Local listen port
    upstream: joycode     # Bound Upstream
    role: cli             # "cli" (default) or "app"
    log_level: simple     # "simple" or "detail"
    modules:
      config_patch:       # Auto-modify ~/.codex/config.toml
        enabled: true
        config_path: ~/.codex/config.toml
      image_filter:       # Filter image_generation tool
        enabled: true
      api_translate:      # Chat Completions ↔ Responses API translation
        enabled: true

# Upstream definitions
upstreams:
  joycode:
    base_url: https://api.joycode.dev/v1
    api_key: sk-...                   # Plain key in config is supported
    api_format: chat_completions       # Requires Chat Completions translation

  openrouter:
    base_url: https://openrouter.ai/api/v1
    api_key: sk-...
    api_format: chat_completions

  openai:
    base_url: https://api.openai.com/v1
    api_key: sk-...                    # Plain key is supported
    # <UPSTREAM_NAME>_API_KEY env var wins over config if set (e.g. OPENAI_API_KEY)
    # No api_format = native Responses API passthrough
```

将 `api_format` 留空或未设置 = 原生直通，无翻译。

`role` 默认为 `"cli"`；带有 `role: app` 的工作进程会被过滤，不出现在 `/launch` 选择器中。`log_level` 默认为 `"simple"`；

`settings.state_dir` 存储 AINN 运行时状态，例如托管终端会话。`settings.log_dir` 存储工作进程日志。

`settings.terminal.tmux.host_start_mode` 默认为 `new-window`。

- `new-window`：保持当前行为
- `reuse-first-window`：在全新的托管 tmux 主机上，将第一个托管会话放置在窗口 `0` 中
- `main-tui-window`：在 tmux 主机内部启动 `./ainn` 本身，并将主 TUI 保留在窗口 `0` 中

`reuse-first-window` 仅影响新创建的托管 tmux 主机。`main-tui-window` 会改变根命令 `./ainn` 的启动方式，并在后续启动时重用配置的 tmux 主机。

### API 密钥解析

对于每个名为 `<NAME>` 的上游，首先检查环境变量 `<NAME>_API_KEY`（例如 `JOYCODE_API_KEY`、`OPENAI_API_KEY`、`OPENROUTER_API_KEY`）。如果环境变量已设置且非空，它将覆盖配置文件中的 `api_key`。

## 测试

```bash
# Go后端
go test ./...

# TUI（终端用户界面）
cd tui && bun test --timeout 30000

# 类型检查
cd tui && bun run typecheck
```

## 子命令

```bash
./ainn version           # 显示版本
./ainn worker ...        # 工作进程（由管理器自动启动，无需手动运行）
./ainn launch --config-dir <dir> --worker <port> [--profile <name>] [--cd <dir>] [--add-dir <dir>] [--model <model>] [--mode <external-window|hosted-terminal>]
                                # 启动连接到工作进程的Codex CLI
                                # --mode hosted-terminal 在AINN拥有的tmux主机中运行Codex（需要tmux）
```

## 待办事项

- [ ] `/status`：在 `/workers` 接管主要工作进程管理流程后，重新引入专用的工作进程状态视图
- [x] 托管终端（实验性）：`/launch` 可以在 AINN 拥有的 `tmux -L ainn` 主机内运行 Codex CLI；AINN 处理 `create` / `switch` / `attach`
- [ ] 嵌入式终端：在 AINN 内部内置 PTY 会话，可直接切换会话

## 许可证

本项目根据 MIT 许可证授权 — 详情请参阅 [LICENSE](../../LICENSE) 文件。

## 归属

本项目是 [anomalyco](https://github.com/anomalyco) 的 [opencode](https://github.com/anomalyco/opencode) 的一个定制分支，在 [MIT 许可证](https://github.com/anomalyco/opencode/blob/main/LICENSE) 下使用。

原始的 opencode 源代码已被修改，以作为 Codex App 的本地代理管理器。

---

<!-- CO-OP TRANSLATOR DISCLAIMER START -->
**免责声明**：
本文件由 AI 翻译服务 [Co-op Translator](https://github.com/Azure/co-op-translator) 翻译完成。尽管我们力求准确，但请注意，自动翻译可能包含错误或不准确之处。原始语言版文件应视为权威来源。对于重要信息，建议使用专业人工翻译。我们对因使用本翻译而产生的任何误解或误释不承担责任。
<!-- CO-OP TRANSLATOR DISCLAIMER END -->