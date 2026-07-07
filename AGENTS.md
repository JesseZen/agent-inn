# AGENTS.md

## 原则

1. 坚持 KISS 原则，方案必须简单直接。
2. 排查 bug 必须定位根本原因，修复源头，而不是随便打补丁。
3. 禁写防御性或"兜底"代码，那样既不解决问题也增加调试成本。
4. 修 bug 时要顺着我现在的设计逻辑来，别动不动就推翻或重写整体逻辑，先按现有实现思路去定位并解决。
5. 代码保持平铺式：按执行顺序直接写在当前上下文，流程清晰、调用栈浅。
6. 默认信任我构造的状态，必要检查提前放在固定位置，别到处加防御性判断或兜底逻辑。

## 代码风格

7. 避免大模块：单文件目标 <500 行（不含测试），超过 800 行必须拆新模块，拆分时把相关测试一起搬走。
8. 不要创建只被引用一次的小 helper 函数，直接内联。（不涵盖常量：为含义不直观的字面量命名是语义需求，不是复用需求。）
9. 避免 bool 参数导致调用点难读（如 foo(false)），优先用 type Mode int + iota 或 functional options。

## 常量与命名

10. 禁止魔法值：出现 2 次以上的字面量或含义不直观的数字/字符串，必须定义为命名常量。同一常量只定义一次，跨包共享放 constants 包。例外：含义自明且仅用一次的值。

## 测试

11. 测试用整体对象相等比较，不要逐字段断言。
12. 不为静态值加测试，不为已删除逻辑加负面测试。
13. 改动核心逻辑优先写集成测试，而非单元测试。
14. Go 测试用 `go test ./...`，TUI 测试用 `cd tui && bun test --timeout 30000`。

## Git

15. 分支名 ≤3 个词，用连字符分隔，不加 type 前缀（如 feat/、fix/）。示例：session-recovery、fix-scroll-state。
16. Commit 和 PR 标题用 conventional commit 格式：type(scope): summary。type 限：feat、fix、docs、chore、refactor、test。scope 可选。
17. 默认分支是 main。对比差异时用 origin/main。
18. 禁止用 force 方式提交被 `.gitignore` 忽略的内容，如 `git add -f`；需要入库忽略内容时先寻求同意，再改 ignore 规则。

## 变更规模

19. 单次变更非机械改动不超过 800 行，复杂逻辑改动不超过 500 行，超出要拆成可审查阶段。

## 版本

20. 版本号由 git tag 驱动，构建时通过 `git describe --tags` 注入。切换 channel：`gh workflow run bump.yml -f channel=beta`

## 调试

21. Worker 日志默认在 `~/.ainn/logs/worker-<port>.log`，可通过 `defaults.log_dir` 配置
22. 另起一个实例测试必须使用独立配置目录和端口：`TEST_CONFIG="$(mktemp -d /tmp/ainn-test.XXXXXX)" && cp ~/.ainn/config.yaml "$TEST_CONFIG/config.yaml"`，然后修改 `$TEST_CONFIG/config.yaml` 里的 `state_dir`、`log_dir`、`settings.terminal.tmux.socket_name`、`settings.terminal.tmux.host_session` 为唯一值；后端用 `./ainn --config-dir "$TEST_CONFIG" --manager-port 19090`，TUI dev 用 `cd tui && AINN_URL=http://127.0.0.1:19090 AINN_CONFIG_DIR="$TEST_CONFIG" bun run dev`，不要复用 `~/.ainn` 或默认 `9090`。

## 人工验证

23. 修 TUI 视觉/交互问题时，自动测试通过后必须提供隔离人工验证命令，让我亲自检查。
24. 人工验证命令必须使用 worktree build 产物、独立临时 config/state/log、非默认 manager port、唯一 tmux socket/session；禁止复用 `~/.ainn`、`9090` 或当前主实例状态。
25. 优先使用最小假配置复现 UI 行为，避免真实 upstream/API key 和真实 worker；最终回复必须包含启动命令、操作路径、预期结果和清理命令。
26. 人工发现的新场景要补自动回归测试，再修源头。
