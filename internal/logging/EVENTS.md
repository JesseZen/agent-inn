# ainn 日志事件手册

> 本文档是 AI 和开发者排查问题的主要参考。每个事件名都是稳定的 grep 目标——遇到某类问题，直接按下面的示例 grep 对应事件，无需读源码。
>
> **日志位置**
> - 主进程：`~/.ainn/logs/ainn.log`（可通过 `config.yaml` 的 `settings.log_dir` 修改）
> - Worker：`~/.ainn/logs/worker-<port>.log`（同一 log_dir）
> - 实时流：TUI 中按 `l` 进入日志面板，或通过 `/api/workers/<port>/logs` SSE 接口

---

## 行格式

```
<timestamp>  <LEVEL>  <component>  <event>  key=value key=value ...
```

| 列 | 说明 |
|---|---|
| timestamp | RFC3339 毫秒精度 UTC，例如 `2026-07-07T14:32:01.123Z` |
| LEVEL | `DEBUG`, `INFO`, `WARN`, `ERROR` 之一，固定 5 字符宽度 |
| component | 点分命名空间，见下方组件表 |
| event | 稳定事件名（本文档的核心），见下方事件表 |
| key=value | 含空格的值加引号；含 token/key 的值自动 redact 为 `***REDACTED***` |

示例：

```
2026-07-07T14:32:01.123Z INFO  worker.proxy  request.start  req=a1b2c3d4 method=POST path=/v1/messages
2026-07-07T14:32:03.456Z INFO  worker.proxy  request.done   req=a1b2c3d4 status=200 dur=3.301s bytes=1204
2026-07-07T14:32:03.500Z ERROR manager.super worker.spawn   worker=claude err="connection refused"
2026-07-07T14:32:10.001Z WARN  manager.health health.fail   worker=claude retries=3
```

---

## 组件（component）

| component | 来源 | 日志文件 |
|---|---|---|
| `root` | 主进程启停 | `ainn.log` |
| `manager.super` | Worker 生命周期 | `ainn.log` |
| `manager.health` | 健康探测循环 | `ainn.log` |
| `manager.api` | 配置 PATCH API | `ainn.log` |
| `worker.proxy` | Worker HTTP 代理 | `worker-<port>.log` |
| `worker.life` | Worker 自身启停 | `worker-<port>.log` |

---

## 事件索引

### root（主进程）

#### `root.start`
- **触发**：主进程 rootRunner 启动，已获取实例锁
- **字段**：`port`（manager HTTP 端口）
- **grep**：`grep root.start ~/.ainn/logs/ainn.log`
- **用途**：确认进程何时启动；多行 root.start 表示进程在短时间内反复重启

#### `root.stop`
- **触发**：rootRunner 正常退出（TUI 关闭、SIGTERM）
- **字段**：无
- **grep**：`grep root.stop ~/.ainn/logs/ainn.log`
- **用途**：确认进程退出是否干净；如果只有 root.start 没有 root.stop，说明进程崩溃

---

### manager.super（Worker 生命周期）

#### `worker.spawn`
- **触发**：manager 成功拉起（INFO）或失败拉起（ERROR）worker 子进程
- **字段**：`worker`（worker 名），`port`（监听端口），`err`（失败时）
- **grep**：`grep worker.spawn ~/.ainn/logs/ainn.log`
- **用途**：排查 worker 为什么起不来；ERROR 级别的 spawn 说明进程创建失败（可执行文件不存在、端口占用等）

#### `worker.exit`
- **触发**：worker 进程停止（StopWorker 调用完成）
- **字段**：`worker`，`status`（stopped/stopped_forced/failed）
- **grep**：`grep worker.exit ~/.ainn/logs/ainn.log`
- **用途**：追踪 worker 是正常停止还是被强制杀掉

#### `worker.restart`
- **触发**：worker 被重启（RestartWorker 完成 stop+start）
- **字段**：`worker`
- **grep**：`grep worker.restart ~/.ainn/logs/ainn.log`
- **用途**：看 worker 重启了多少次；配合 health.fail 可以看出是健康检查触发的自动重启

#### `hosted_turn.poll`
- **触发**：Hosted terminal turn watcher 轮询 Codex transcript 时发生错误
- **字段**：`error`
- **LEVEL**：WARN
- **grep**：`grep hosted_turn.poll ~/.ainn/logs/ainn.log`
- **用途**：排查绿色/蓝色 tab 状态没有按 transcript 纠偏的问题；常见原因是 transcript JSONL 损坏、文件权限异常或 tmux 状态更新失败

---

### manager.health（健康探测）

#### `health.fail`
- **触发**：对某个 worker 的健康探测失败，retries 计数递增
- **字段**：`worker`，`retries`（累计失败次数，≥10 时 manager 将 worker 标记为 failed）
- **LEVEL**：WARN
- **grep**：`grep health.fail ~/.ainn/logs/ainn.log`
- **用途**：排查 worker 不健康的根本原因。看到 retries 递增但 worker 还活着，通常是 worker 在启动中；retries=10 时会停止重试

---

### worker.proxy（请求路径）

所有 worker.proxy 事件都带 `req=<8-hex-id>` 关联 ID，同一请求的所有行共享同一 req 值。

#### `request.start`
- **触发**：worker 收到新的代理请求，在处理前
- **字段**：`req`，`method`，`path`
- **LEVEL**：INFO（detail 模式可见，simple 模式不记录成功请求）
- **grep**：`grep request.start ~/.ainn/logs/worker-<port>.log`
- **用途**：看请求是否到达了 worker；没有 request.start 说明请求没进到这个 worker

#### `request.done`
- **触发**：请求完全处理完，响应已写回客户端
- **字段**：`req`，`method`，`path`，`status`（HTTP 状态码），`dur`（总耗时），`bytes`（响应字节）或 `err`（失败时）
- **LEVEL**：INFO（2xx）/ WARN（4xx）/ ERROR（5xx 或 err）
- **grep 慢请求**：`grep request.done ~/.ainn/logs/worker-<port>.log | grep -v ' dur=[0-2]'`
- **grep 失败请求**：`grep request.done ~/.ainn/logs/worker-<port>.log | grep -E 'ERROR|WARN'`
- **用途**：配合 req= 追一次完整请求的耗时和结果；出现 err= 字段说明请求处理中断

#### `upstream.fail`
- **触发**：向上游服务发送请求时 HTTP 客户端报错（网络错误、连接拒绝、超时等）
- **字段**：`req`，`method`，`path`，`url`（上游 URL），`err`
- **LEVEL**：ERROR
- **grep**：`grep upstream.fail ~/.ainn/logs/worker-<port>.log`
- **用途**：上游不通时会出现这行。err 字段描述了具体原因（connection refused / i/o timeout 等）

#### `module.fail`
- **触发**：请求处理链中某个模块（ProcessRequest 或 WrapResponse）返回错误
- **字段**：`req`，`module`（模块名），`method`，`path`，`err`，`phase`（wrap_response 时补充）
- **LEVEL**：ERROR
- **grep**：`grep module.fail ~/.ainn/logs/worker-<port>.log`
- **用途**：找出是哪个模块导致请求失败。`module=` 字段直接指向模块名

#### `snapshot.reload`
- **触发**：worker 的运行时配置被热更新（UpdateRuntime 调用成功）
- **字段**：`generation`（新的快照版本号）
- **LEVEL**：INFO
- **grep**：`grep snapshot.reload ~/.ainn/logs/worker-<port>.log`
- **用途**：确认配置变更是否已经下发到 worker

---

## 日志级别说明

| `log_level` 配置值 | simple 模式（默认） | detail 模式 |
|---|---|---|
| 对应 slog 级别 | INFO+ | DEBUG+ |
| 过滤说明 | 只保留 WARN/ERROR；INFO 的 request.start/done 被过滤 | 全量保留 |
| 适用场景 | 生产/日常使用，日志量小 | 排查特定请求路径、模块行为 |

**修改 worker 日志级别**：在 `config.yaml` 的 `workers.<name>.log_level` 设置为 `detail`，TUI 重载配置后即时生效（无需重启 worker）。

**修改主进程日志级别**：在 `config.yaml` 的 `settings.log_level` 设置为 `detail`，重启主进程后生效。

---

## 常见问题排查速查

### Worker 起不来

```bash
# 1. 看主进程日志里的 spawn 失败
grep -E "worker.spawn|worker.exit|health.fail" ~/.ainn/logs/ainn.log | tail -40

# 2. 看 worker 自身日志（进程起来了但出错）
tail -100 ~/.ainn/logs/worker-<port>.log
```

### 请求报错 / 上游 401

```bash
# 找所有上游失败，看 err 字段
grep upstream.fail ~/.ainn/logs/worker-<port>.log | tail -20

# 找某个 req 的完整链路
grep req=<id> ~/.ainn/logs/worker-<port>.log
```

### 请求很慢 / 卡住

```bash
# 找耗时超过 10s 的请求（dur 大于 10s 的行）
grep request.done ~/.ainn/logs/worker-<port>.log | grep -vE 'dur=[0-9]\.'

# 看有没有 request.start 但没有 request.done（开启 detail 模式后可见）
```

### Hosted terminal tab 一直显示 running

```bash
# 1. 先确认 hook 是否安装
./ainn hooks status

# 2. 确认 hosted session 记录里的 turn 状态
cat ~/.ainn/hosted-terminal-sessions.json

# 3. Codex 场景再检查 session 记录里的 turn_transcript_path 对应 JSONL
#    搜对应 turn_id 的 task_complete，last_agent_message=null 表示失败完成
grep '"task_complete"' <transcript_path> | tail -20

# 4. 看 manager watcher 是否轮询 transcript 时报错
grep hosted_turn.poll ~/.ainn/logs/ainn.log | tail -20
```

**判断原则**：`UserPromptSubmit` 只表示 turn 已开始；`Stop` 表示 launcher 认为已结束；Claude 的 `StopFailure` 表示 API 错误结束；Codex 没有等价失败 hook，因此 AINN 优先记录 hook input 中的 `transcript_path` 和 `turn_id`，缺失时由 manager turn watcher 通过 `launcher_session_id` 反查 transcript 并补充判定 failed/interrupted 结果。若 tab 长时间停在蓝色 running，优先检查对应 launcher 是否没有写出 terminal 事件，或 session 记录里的 `launcher_session_id` 是否无法匹配到 `~/.codex/sessions` 下的 JSONL。

如果 running 的父会话被 subagent 结束事件标成绿色 done，检查 hook input 是否带 `agent_id`、`agent_transcript_path` 或 `hook_event_name=SubagentStop`；这类 subagent payload 不应修改父 hosted session 状态。

### 配置没生效

```bash
# 确认 worker 收到了热更
grep snapshot.reload ~/.ainn/logs/worker-<port>.log

# 确认 manager 成功 spawn 了新 worker（若端口变了会重新 spawn）
grep worker.spawn ~/.ainn/logs/ainn.log | tail -10
```

### Worker 反复重启

```bash
# 看重启次数和健康失败计数
grep -E "worker.restart|health.fail" ~/.ainn/logs/ainn.log | tail -20
```

---

## 轮转规则

- `ainn.log` 超过 10MB 时轮转为 `ainn.log.1`，最多保留 3 个备份（`ainn.log.1`、`.2`、`.3`）
- `worker-<port>.log` 同上（每个 worker 独立计算）
- 内存环形缓冲区保留最近 1000 行（供 TUI 日志面板展示）

---

*最后更新：2026-07-07*
