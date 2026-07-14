# ainn 日志事件手册

> 本文档是 AI 和开发者排查问题的主要参考。每个事件名都是稳定的 grep 目标——遇到某类问题，直接按下面的示例 grep 对应事件，无需读源码。
>
> **日志位置**
> - 主进程：`~/.ainn/logs/ainn.log`（可通过 `config.yaml` 的 `settings.log_dir` 修改）
> - Worker：`~/.ainn/logs/worker-<port>.log`（同一 log_dir）
> - tmux 生命周期：`~/.ainn/logs/tmux-<socket>.log`（同一 log_dir）
> - 崩溃证据：`~/.ainn/logs/crashes/root-<UTC>-<run>.log`（同一 log_dir）
> - 未完成运行标记：`~/.ainn/logs/crashes/active-root.json`
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
| `root.supervisor` | root 子进程 stderr、信号和实际退出状态 | `crashes/root-*.log` |
| `tmux.supervisor` | AINN 私有 tmux server 和 attach client 生命周期 | `tmux-<socket>.log` |
| `manager.super` | Worker 生命周期 | `ainn.log` |
| `manager.health` | 健康探测循环 | `ainn.log` |
| `manager.api` | 配置 PATCH API | `ainn.log` |
| `worker.proxy` | Worker HTTP 代理 | `worker-<port>.log` |
| `worker.life` | Worker 自身启停 | `worker-<port>.log` |

---

## 闪退排查第一步

不要先从业务请求日志猜原因。先打开最新 crash artifact，再用其中的
`run=<id>` 关联主进程和 worker 日志：

```bash
LOG_DIR="$HOME/.ainn/logs" # 使用自定义 settings.log_dir 时替换这里
CRASH="$(find "$LOG_DIR/crashes" -name 'root-*.log' -type f 2>/dev/null | sort -r | head -1)"
echo "crash=$CRASH"
tail -200 "$CRASH"

RUN="$(grep -Eo 'run=[0-9a-f]+' "$CRASH" | head -1 | cut -d= -f2)"
grep "run=$RUN" "$LOG_DIR/ainn.log" "$LOG_DIR"/worker-*.log 2>/dev/null | tail -300
```

### 事件组合判定

| 证据 | 结论 |
|---|---|
| `root.panic`，随后 crash artifact 有 Go stack | root 主 goroutine panic；按 stack 第一条项目内路径定位 |
| crash artifact 有 `panic:` / `fatal error:`，但 `ainn.log` 没有 `root.panic` | 后台 goroutine panic 或 Go runtime fatal；以 artifact 的 all-goroutine stack 为准 |
| `root.supervisor.exit reason=signal signal=killed` | root 子进程收到 `SIGKILL`；不是 TUI 正常退出 |
| `root.signal` + `root.stop reason=signal` | root 收到并完成了 `SIGINT/SIGTERM/SIGHUP` 的有序关闭 |
| `root.supervisor.exit ... forwarded_signal=quit` | `SIGQUIT` 已转发给 root；macOS Go runtime 可能以 `exit_code=2` 结束，crash artifact 的全 goroutine dump 才是关键证据 |
| `tui.exit reason=tui_exit exit_code!=0` + `root.stop` | Bun/TUI 子进程失败，root 仍完成了清理；查看同一 artifact 中 TUI stderr |
| `root.previous_unclean` | 上一次连 supervisor 都没能记录退出，常见于 supervisor 自身被 `SIGKILL`、tmux server 被杀或断电 |
| `tmux.client.exit reason=server_unexpected` + `tmux.server.exit reason=signal signal=killed` | tmux server 收到 `SIGKILL`；`initiator=external_or_unknown` 表示不是 AINN supervisor 转发的信号 |
| `tmux.client.exit reason=server_unexpected` + `tmux.server.exit reason=signal signal="segmentation fault"` | tmux server 因崩溃信号退出 |
| `tmux.client.exit reason=server_terminated` + `tmux.server.signal signal=terminated` | tmux supervisor 收到外部 `SIGTERM` 并转发给 server；server 处理后可能以 `tmux.server.exit reason=clean exit_code=0` 返回 |
| `worker.exit exit_code!=0` 或 `signal` 非空 | worker 真实异常退出；用同一 `run` 和 `worker` 查看对应 `worker-<port>.log` |
| 只有请求 `503`，同一时间没有 root/worker 日志 | 先确认 root 是否已退出；整个 manager 消失时 pool 无法执行故障转移 |

`SIGKILL` 无法由被杀进程自己处理。AINN 通过外层 supervisor 的 wait status
记录 root 子进程的 `SIGKILL`；若 supervisor 也被同时杀掉，则下一次启动通过
遗留的 `active-root.json` 产生 `root.previous_unclean`。

tmux 的 wait status 同样不包含发信号者 PID。`initiator=ainn` 只在 AINN 的
tmux supervisor 因启动/setup/信号转发失败而主动清理 server 时出现；supervisor
收到外部信号后正常转发仍记为 `external_or_unknown`。该值无法区分其他进程、
用户操作和操作系统，精确追踪外部发信号者需要特权系统审计。

---

## 事件索引

### root.supervisor（进程边界）

#### `root.supervisor.start`
- **触发**：外层 supervisor 已建立本次 crash artifact 和 active marker
- **字段**：`run`，`pid`（supervisor PID），`started_at`，`version`，`go`，`os`，`arch`，`config_dir`，`port`，`artifact`
- **位置**：仅 `crashes/root-*.log`
- **用途**：确定运行身份、构建版本、平台和所有后续日志的关联 ID

#### `root.supervisor.child`
- **触发**：真实 root 子进程启动成功
- **字段**：`run`，`child_pid`
- **用途**：区分 supervisor PID 与实际 manager/TUI PID

#### `root.supervisor.signal`
- **触发**：supervisor 收到 `SIGINT/SIGTERM/SIGHUP/SIGQUIT` 并转发给 root 子进程
- **字段**：`run`，`signal`，`child_pid`，转发失败时有 `err`
- **LEVEL**：WARN；转发失败为 ERROR
- **用途**：确认信号是否先到 supervisor，以及信号是否成功转发；`SIGQUIT` 会保留 Go runtime 的全 goroutine dump

#### `root.supervisor.exit`
- **触发**：supervisor 已 `Wait` 到 root 子进程的真实退出状态
- **字段**：`run`，`child_pid`，`exit_code`，`reason`（`clean/exit_code/signal/start_error/wait_error`），`error`，`signal`，`forwarded_signal`，`duration_ms`，`completed_at`
- **用途**：这是判断 root 为什么消失的权威事件；`signal` 非空优先于 exit code
- **进程清理**：root 退出后 supervisor 会终止同组残留的 TUI 后代，再完成 stderr 落盘；残留后代不会卡住退出事件

#### `root.previous_unclean`
- **触发**：新一轮启动发现上一轮遗留 `active-root.json`
- **字段**：`run`（当前），`previous_run`，`previous_pid`，`previous_started_at`，`previous_artifact`
- **LEVEL**：WARN
- **用途**：说明上一轮 supervisor 未执行到退出记录；直接打开 `previous_artifact`

### tmux.supervisor（tmux 进程边界）

#### `tmux.server.start`
- **触发**：AINN 的 tmux supervisor 已以前台模式启动新的私有 tmux server
- **字段**：`pid`，`supervisor_pid`，`socket`，`host_session`，`config_dir`，`started_at`
- **位置**：`tmux-<socket>.log`
- **用途**：确认后续退出事件对应的真实 tmux server PID；已存在的旧 server 不会被主动替换，需等下一次自然重建后才有此事件
- **边界**：`can't find session` 但 server 仍存在时只创建缺失 session，不会重启 server；只有 `no server running` 或 socket 不存在才启动新的受管 server

#### `tmux.server.signal`
- **触发**：AINN 的 tmux supervisor 即将向 server 转发信号
- **字段**：`pid`，`signal`，`initiator`（`ainn/external_or_unknown`）
- **LEVEL**：WARN
- **用途**：证明信号经过 AINN supervisor 转发；`initiator=ainn` 表示 supervisor 因启动/setup/转发失败主动清理，收到外部信号再正常转发则为 `external_or_unknown`

#### `tmux.server.exit`
- **触发**：tmux supervisor 已 `Wait` 到 server 的真实退出状态
- **字段**：`pid`，`exit_code`，`reason`（`clean/exit_code/signal/start_error/wait_error`），`signal`，`initiator`（`ainn/external_or_unknown`），`duration_ms`，`completed_at`，`error`，可选 `output_tail`
- **用途**：判断 tmux server 是正常空会话退出、非零返回、被 `SIGKILL`，还是因崩溃信号退出；`output_tail` 有长度上限并经过凭据脱敏

#### `tmux.client.exit`
- **触发**：AINN 的 tmux attach client 返回
- **字段**：`socket`，`host_session`，`reason`（`detached/empty/server_terminated/server_unexpected/client_error`），`exit_code`，`error`
- **用途**：保留 tmux 文档定义的 client 视角；`server_unexpected` 必须与同一 `tmux-<socket>.log` 中紧邻的 `tmux.server.exit` 联合判断
- **注意**：AINN 不启用 tmux `-v`，因为原生日志会包含完整 client 环境变量

tmux server 意外退出时直接按配置的 socket 查看同一文件；主 TUI 和
hosted-terminal 首次创建 server 都写入这里：

```bash
SOCKET=ainn # 使用 settings.terminal.tmux.socket_name 的实际值
grep -E 'tmux\.(server|client)\.(start|signal|exit)' "$HOME/.ainn/logs/tmux-$SOCKET.log" | tail -40
```

### root（主进程与 TUI）

#### `root.start`
- **触发**：真实 root 子进程已打开 `ainn.log`
- **字段**：`run`，`pid`，`ppid`，`version`，`go`，`os`，`arch`，`config_dir`，`port`，`crash_path`
- **grep**：`grep root.start ~/.ainn/logs/ainn.log`
- **用途**：确认 manager/TUI 实际启动；用 `run` 关联 crash 和 worker 日志

#### `root.signal`
- **触发**：root 收到 `SIGINT/SIGTERM/SIGHUP`，或检测到 supervisor pipe 关闭
- **字段**：`run`，`reason`（`signal/supervisor_lost`），`signal`
- **LEVEL**：WARN
- **用途**：区分有序信号关闭和 supervisor 异常消失；`SIGHUP` 后紧跟 `root.stack`

#### `root.stack`
- **触发**：root 收到 `SIGHUP`
- **字段**：`run`，`reason=hangup`
- **用途**：事件后完整 goroutine stack 写入同一 crash artifact

#### `root.panic`
- **触发**：root 主 goroutine panic，被记录后立即重新 panic
- **字段**：`run`，`panic`，`pid`
- **LEVEL**：ERROR
- **用途**：不恢复执行；完整 stack 在 `crash_path` 指向的 artifact

#### `tui.start`
- **触发**：root 即将启动 Bun TUI
- **字段**：`run`，`command`
- **用途**：确认 manager 已进入 TUI 阶段

#### `tui.exit`
- **触发**：Bun TUI 返回或被 root 取消
- **字段**：`run`，`reason`（`tui_exit/root_signal/server_error`），`exit_code`，`signal`，失败时有 `err`
- **LEVEL**：正常为 INFO，非零退出为 ERROR
- **用途**：区分 TUI 自身崩溃与 root/manager 崩溃

#### `root.stop`
- **触发**：root 已完成明确的有序返回路径；panic/fatal 不写此事件
- **字段**：`run`，`reason`（`tui_exit/signal/supervisor_lost/server_error`），信号退出有 `signal`，错误退出有 `err`
- **grep**：`grep root.stop ~/.ainn/logs/ainn.log`
- **用途**：`root.start` 无对应 `root.stop` 表示 root 没走完清理；再以 supervisor artifact 确认原因

---

### manager.super（Worker 生命周期）

#### `worker.spawn`
- **触发**：manager 成功拉起（INFO）或失败拉起（ERROR）worker 子进程
- **字段**：`worker`（worker 名），`port`（监听端口），`err`（失败时）
- **grep**：`grep worker.spawn ~/.ainn/logs/ainn.log`
- **用途**：排查 worker 为什么起不来；ERROR 级别的 spawn 说明进程创建失败（可执行文件不存在、端口占用等）

#### `worker.exit`
- **触发**：worker 进程停止（StopWorker 调用完成）
- **字段**：`run`，`worker`，`status`（stopped/stopped_forced/failed），`exit_code`，`signal`，`forced`，`process_error`
- **grep**：`grep worker.exit ~/.ainn/logs/ainn.log`
- **用途**：追踪 worker 是正常停止还是被强制杀掉

---

### worker.life（Worker 进程边界）

这些低频生命周期 INFO 即使在 `simple` 模式也会保留。

#### `worker.start`
- **触发**：worker 已解析运行时配置并开始构建模块
- **字段**：`run`，`worker`，`pid`，`port`，`generation`

#### `worker.ready`
- **触发**：模块和 hooks 已构建，worker 即将进入 HTTP Serve
- **字段**：同 `worker.start`

#### `worker.signal`
- **触发**：worker 收到 `SIGINT/SIGTERM`
- **字段**：同 `worker.start`，另有 `signal`
- **LEVEL**：WARN

#### `worker.stop`
- **触发**：worker server 返回并完成 defer
- **字段**：同 `worker.start`，`reason=clean/error`，错误时有 `err`
- **用途**：缺少此事件但 worker log 尾部有 panic/runtime stack，说明 worker 非有序退出

#### `worker.restart`
- **触发**：worker 被重启（RestartWorker 完成 stop+start）
- **字段**：`worker`
- **grep**：`grep worker.restart ~/.ainn/logs/ainn.log`
- **用途**：看 worker 重启了多少次；配合 health.fail 可以看出是健康检查触发的自动重启

#### `metrics.persist`
- **触发**：manager 收到 worker 指标事件，但写入累计指标文件失败
- **字段**：`worker`（worker 名），`port`（监听端口），`err`
- **LEVEL**：ERROR
- **grep**：`grep metrics.persist ~/.ainn/logs/ainn.log`
- **用途**：排查实时指标仍更新但累计指标未持久化的问题；常见原因是 `state_dir` 不可写或路径被文件占用

#### `upstream.failover`
- **触发**：Manager 已收到结构化上游故障或恢复探测，但切换 pool 中的 worker runtime 失败
- **字段**：`upstream`，`worker`（由请求触发时），`err`
- **LEVEL**：ERROR
- **grep**：`grep upstream.failover ~/.ainn/logs/ainn.log`
- **用途**：排查熔断状态已变化但 worker 仍停留在旧 upstream，通常表示 runtime 热更新失败

#### `hosted_turn.poll`
- **触发**：Hosted terminal turn watcher 轮询 Codex transcript 时发生错误
- **字段**：`category`（`transcript_read`、`transcript_parse`、`registry_write`、`tmux_projection`、`snapshot_reconciliation` 或 `poll`），安全时有 `path`、`position`、`session_id`
- **LEVEL**：WARN
- **grep**：`grep hosted_turn.poll ~/.ainn/logs/ainn.log`
- **用途**：排查绿色/蓝色 tab 状态没有按 transcript 纠偏的问题；常见原因是 transcript JSONL 损坏、文件权限异常或 tmux 状态更新失败
- **隐私**：不写入底层错误文本、request call ID、问题/答案、JSONL 内容或 tmux 输出；`path` 和 `session_id` 只用于定位归属

#### `hosted_turn.ownership`
- **触发**：root 在安全重启或 sidecar handoff 中解析/获取 hosted watcher 独占锁失败
- **字段**：`category`（`lock_path` 或 `handoff_timeout`），`path`，超时场景有 `timeout_ms`
- **LEVEL**：ERROR
- **grep**：`grep hosted_turn.ownership ~/.ainn/logs/ainn.log`
- **用途**：确认 watcher ownership 是否在 root 重启前完成交接；不记录底层锁错误文本

---

### manager.health（健康探测）

#### `health.fail`
- **触发**：对某个 worker 的健康探测失败，retries 计数递增
- **字段**：`worker`，`retries`（累计失败次数，≥10 时 manager 将 worker 标记为 failed）
- **LEVEL**：WARN
- **grep**：`grep health.fail ~/.ainn/logs/ainn.log`
- **用途**：排查 worker 不健康的根本原因。看到 retries 递增但 worker 还活着，通常是 worker 在启动中；retries=10 时会停止重试

---

### Manager SSE（上游探测与池自适应探测）

以下事件来自 Manager 的 `/api/events` SSE 流，不是结构化日志行，不会写入
`ainn.log`。因此下面两条日志 grep 不会显示这些事件：

```bash
grep 'upstream.probed' ~/.ainn/logs/ainn.log
grep 'upstream.pool.mode.changed' ~/.ainn/logs/ainn.log
```

实时检查时，`AINN_MANAGER_PORT` 必须设置为待排查实例实际使用的 manager port：

```bash
curl -N "http://127.0.0.1:${AINN_MANAGER_PORT}/api/events" | grep 'event: upstream.probed'
curl -N "http://127.0.0.1:${AINN_MANAGER_PORT}/api/events" | grep 'event: upstream.pool.mode.changed'
curl -N "http://127.0.0.1:${AINN_MANAGER_PORT}/api/events" | grep 'event: upstream.pool.state.changed'
```

#### `upstream.probed`
- **公共字段**：`upstream`，`mode`，`authoritative`，`readiness`，`ok`，`status_code`，`latency_ms`；按结果可选 `degraded`、`error`
- **独立 Test Upstream 事件**：由手动测试单个 upstream 或全部 upstream 触发；`authoritative=false`，`readiness=unknown`，只含上述公共字段，不含 `pool`、`eligible`、`checked_at`、`stale`、`probe_state`、`next_probe_at`、`reason`
- **pool 自适应事件**：由 Manager 完成 pool 成员的权威协议探测或 readiness 过期触发；`authoritative=true`，除公共字段外固定含 `pool`、`eligible`、`checked_at`、`probe_state`、`reason`，有调度截止时间时含 `next_probe_at`，过期时含 `stale`
- **状态**：`probe_state=paused` 表示 pool 已禁用，`idle` 表示没有附属 worker；两者都没有计划中的协议调用。`stable` 使用稳定周期，`alert` 使用告警周期并可按连续失败次数退避
- **原因**：`reason` 为 `startup/stable/worker_failure/recovery/manual/config` 之一，表示本次探测的调度来源
- **用途**：结合 `readiness`、`eligible`、`next_probe_at` 判断成员能否被自动或普通手动切换，以及下一次探测何时发生。协议探测响应不用于推断精确 token 用量

#### `upstream.pool.mode.changed`
- **触发**：pool 在 `active` 与 `disabled` 间切换
- **字段**：`pool`，`previous_mode`，`mode`
- **用途**：确认禁用或重新启用自适应故障转移的配置变更已由 Manager 接收

#### `upstream.pool.state.changed`
- **触发**：worker 请求结果改变 pool 派生的探测状态或下一次探测时间
- **字段**：`pool`，`probe_state`，有调度截止时间时含 `next_probe_at`
- **用途**：请求结果不是协议探测；该事件只投影 pool 汇总状态，不携带 readiness 或探测结果

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
| 过滤说明 | 保留 WARN/ERROR、未知原始输出及 `worker.life`；INFO 的 request.start/done 被过滤 | 全量保留 |
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

### AINN 整体闪退 / 远程看到 503

```bash
# 1. 先看 root 子进程真实退出状态和 stderr/stack
find ~/.ainn/logs/crashes -name 'root-*.log' -type f | sort -r | head
tail -200 "$(find ~/.ainn/logs/crashes -name 'root-*.log' -type f | sort -r | head -1)"

# 2. 看是否走完 root 清理
grep -E 'root\.(start|signal|panic|stack|stop)|tui\.(start|exit)' ~/.ainn/logs/ainn.log | tail -80

# 3. 看 worker 是被 root 有序停止，还是自身先崩溃
grep -E 'worker\.(spawn|exit)|health.fail' ~/.ainn/logs/ainn.log | tail -80
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
- 每次 root 运行有独立 `crashes/root-<UTC>-<run>.log`，单文件 10MB、2 个备份，保留最近 10 个 run 组
- 内存环形缓冲区保留最近 1000 行（供 TUI 日志面板展示）

---

*最后更新：2026-07-12*
