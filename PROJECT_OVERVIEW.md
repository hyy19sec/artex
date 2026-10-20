# ARTEX 项目通读梳理

> 基于仓库快照（CHANGELOG 最新 `0.3.15`，2026-10-07）整理。

## 目录

- [一、项目定位](#一项目定位)
- [二、技术栈与规模](#二技术栈与规模)
- [三、目录与模块职责](#三目录与模块职责)
- [四、核心架构](#四核心架构)
- [五、数据模型](#五数据模型dbsschemasql1386-行)
- [六、HTTP API 与前端](#六http-api-与前端)
- [七、运行方式](#七运行方式)
- [八、当前工作区状态与注意点](#八当前工作区状态与注意点)

---

## 一、项目定位

**ARTEX** 是一个 **AI 自主渗透测试系统**（AGPL-3.0），核心是"LLM 多 Agent 驱动 + 双图架构"：由 planner 决定探索方向、worker 用真实工具执行、把结果写回图，形成事件驱动的闭环。README 明确限定仅供本地隔离环境学习研究。

## 二、技术栈与规模

| 项 | 内容 |
|---|---|
| 后端 | Go 1.26.3，`/cmd/artex` 单二进制，`net/http` 原生路由 + JWT + SSE |
| Agent SDK | `github.com/Autumn-27/norma v0.4.3`（agentcore / tool / permission / hook / harness / memory / transcript） |
| 数据库 | PostgreSQL（pgx v5），schema 由 `go:embed` 内嵌，每次启动幂等迁移 |
| 存储 | SQLite 仅用于流量索引（`modernc.org/sqlite`）；资产/探索数据全在 PG |
| 前端 | Next.js 16 + React 19 + Tailwind 4 + shadcn/radix + AntV G6/XYFlow + zustand + recharts |
| 其他 | go-mitmproxy（流量录制）、dnsx（DNS 补全）、excelize（导出）、JWT |
| 规模 | Go：328 文件 / 约 9.4 万行；前端：152 个 tsx / 约 5.6 万行 |
| 版本 | CHANGELOG 最新 `0.3.15`（2026-10-07），前端 package `2.2.0` |

## 三、目录与模块职责

```text
cmd/artex/      入口：参数、banner、日志捕获、自更新 bootstrap、HTTP server、优雅关闭
server/         最大模块（104 文件）：Manager 生命周期、Engine 调度、REST/SSE、
                orchestrator、scheduler、intercept API、任务归档、资产同步、LLM 管理
agent/          规划者/执行者/主 agent/目标拆解/复测 agent、ToolSet 领域工具、提示词
db/             PostgreSQL store（assets/exploration/tasks/findings/…）+ schema.sql
guard/          安全边界：审计 + PreToolUse 钩子
intercept/      可配置拦截规则（allow/deny/ask）、人工审批等待
traffic/        MITM 录制代理（默认 :8788）：文件树 + SQLite 索引、证据快照
enrich/         非 AI 异步补全：DNS 解析 + HTTP 探测回写资产图
llmpool/        LLM 配置链：优先级、故障切换、上下文窗口过滤
llmrec/         LLM 原始请求/响应留痕
report/         漏洞报告导出
notify/         6 类 IM 推送渠道（钉钉/飞书/企微/Webhook/Telegram/邮件）
mcphttp/        MCP Streamable HTTP / SSE 客户端
selfupdate/     页面一键更新：下载、SHA256、冒烟、换装、失败回滚
evidence/       证据存储
sidequestion/   `/btw` 旁路提问
skills/         内置技能：api-recon、playwright-cli、scopesentry
web/            Next.js 前端（静态导出后 go:embed）
```

## 四、核心架构

### 1. 双图模型

- **资产图（全局共享）**：`assets` 表，类型 `root_domain → subdomain → ip / service / app → endpoint`，归属 `companies`，父子关系与去重由程序计算。这是跨任务的资产真值库。
- **探索图（每任务独立）**：`exploration_nodes`，kind 为 `begin / goal / intent / fact / finding / hint / digest`；边为 `spawns / derived_from / yields / proves / covers`，构成血缘链。
- **锚点**：`exploration_anchors(node_id, asset_id)` 把探索节点挂到具体资产，支撑覆盖度统计和资产覆盖图。

### 2. Agent 分工

| Agent | 职责 |
|---|---|
| `goals` | 目标拆解器：从任务描述抽取最终目标和 allow/deny 操作约束 |
| `planner` | **唯一意图生成者**；事件驱动，每轮全新会话，但有跨唤醒共享 todolist |
| `worker` | 领取一条 intent → 真实工具执行 → 写回 fact/asset/finding → 停 |
| `mainagent` | 人在环路：聊天、加目标/提示、steer/kill worker |
| `retester` | 漏洞复测，独立会话 |
| `auto` / `pentest` / 自定义 | 资产/漏洞管理型 agent |

关键设计在 `agent/planner.go:298` 的提示词与 `server/engine.go:800` 的 `plannerLoop`：

- 图变更 debounce 后唤醒 planner；心跳兜底（默认 ≥600s）。
- **硬底线**：目标未达成且无 open/running 意图时必须派至少 1 条新意图。
- 串行利用链用 `TodoWrite` 分轮推进，避免一次性并行下发。
- `prove_goal` 标记最后一个目标 → 任务自动完成；`goal_met` 默认不绑定。

### 3. 执行引擎（`server/engine.go`）

每个任务 = 1 个 planner goroutine + N 个 worker goroutine（默认 3，可在系统设置改）：

- `claimNext` 从 frontier 按 priority 领 open intent（`running` 状态 CAS）。
- 支持 pause / resume / stop / delete barrier / steer / kill / drain / 任务级超时收尾。
- LLM 调用会计数（`BeginLLMCall`），活动流按 `activity` 表落库并 SSE 广播。

### 4. 工具与权限

- `ToolSet`（`agent/tools.go`）把双图暴露成工具：`graph_overview`、`list_facts`、`node_detail`、`add_intent`、`prove_goal`、`report_finding`、`record_fact`、`insert_assets`、`search_all_worker_traces` 等。
- 内置工具会幂等播种进 `tools` 表，UI 可覆盖描述/schema/绑定 agent（handler 仍在代码层）。
- 通用工具 Read/Write/Edit/Glob/Grep/Bash + 可选交互式 shell（`shell_open` 等）、WebFetch/WebSearch。
- **guard + intercept**：每次工具调用过 PreToolUse 钩子，按 DB 规则优先级执行 allow/deny/ask；`ask` 会挂起等待前端审批（`intercept_pending`）；支持模型兜底审批。

### 5. 流量与证据

- 内嵌 MITM 代理对所有 worker 的 HTTP 留痕（文件树 + SQLite 索引，分页查询）。
- 漏洞可绑定流量快照（`finding_traffic_bindings`），支持证据查看与导出。

## 五、数据模型（`db/schema.sql`，1386 行）

约 50 张表，主要分组：

- 资产：`companies / assets / company_scope`
- 任务：`tasks / task_scope / task_assets / task_assets_links / task_categories / task_templates / task_archives / task_relations / task_llm_profiles / scheduler_state`
- 探索：`explorations / exploration_nodes / exploration_edges / exploration_anchors / activity / task_constraints`
- 漏洞：`findings / finding_retests / traffic_evidence_snapshots / finding_traffic_bindings`
- Agent：`agents / agent_prompts / agent_prompt_vars / agent_visibility / agent_skill_visibility / agent_triggers / tools / tool_usage / skill_usage`
- LLM：`llm_profiles / llm_profile_health`
- 拦截：`intercept_rules / intercept_pending / asset_intercept_rules / task_intercept_rules`
- 通知：`notification_channels / notification_events / notification_deliveries`
- 其他：`conversations / conversation_activities / main_sessions / mcp_servers / mcp_tools_cache / side_question_sessions / side_question_requests / server_logs / settings`

## 六、HTTP API 与前端

- API 全部挂在 `/api/*`（`server/server.go:655` 起），含 auth、tasks、assets、goals、constraints、intents 控制、intercept、findings、traffic、MCP、skills、LLM、notify、update、logs(SSE)、workspace 等。
- 前端页面：
  - 业务：`/dashboard`、`/chat`、`/function/{tasks,assets,findings,traffic,commands,llm-records,sync,workspace}`
  - 系统：`/system/{agents,tools,skills,mcp,llm,intercept,notify,logs,settings}`
  - 认证：`/login`、`/setup`

## 七、运行方式

- 推荐 `./install.sh`（Docker 全栈或本地编译）；或 `docker compose up -d`；或下载 Release 后 `./start.sh` / `start.bat`。
- 开发：`./dev.sh` = 后端 `:8787` + 流量代理 `:8788` + `next dev :5173`。
- 构建：`cd web && npm run build:static` → 拷 `web/out` 到 `server/webui/dist` → `go build -tags embedui`。
- 更新：页面一键更新由 `selfupdate` 完成换装，退出码 75 通知 `start.sh/bat` 重新拉起，连续 3 次启动失败自动回滚。

## 八、当前工作区状态与注意点

1. **不是 git 仓库**（无 `.git`），无法看 diff/历史。
2. 当前环境 **Go 未加入 PATH**（`go: command not found`），Node v22 / npm 10 可用，但 `web/node_modules` 未安装，暂时无法直接编译或跑测试。
3. 没有 `config.json`、`data/`、`.env`，即尚未初始化运行环境（需 PostgreSQL）。
4. 多处注释引用的 `docs/*.md` 在 `.gitignore` 中被忽略，因此本快照里不存在（如 `docs/交互式shell设计.md`）。
5. `cmd/artex/main.go:1` 的包注释写的是 "dual SQLite graph stores"，与现状不符——图数据已全部在 PostgreSQL，SQLite 只剩流量索引，属于过时注释。
6. `.gitignore` 忽略了 `/docs/` 与 `.claude/`，说明本地开发文档与 Claude 配置不随仓库分发。
