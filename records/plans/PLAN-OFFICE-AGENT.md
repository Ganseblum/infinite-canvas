---
plan_id: PLAN-OFFICE-AGENT
plan_version: 1
status: proposed
objective: 基于 Claude Agent SDK 在现有平台落地「AI 办公助理」Agent 产品线：M0 spike 验证全部待验证假设（G0 门）→ M1 端到端最小链路 → M2 工具工作区与 Artifacts → M3 模型矩阵与计费闭环；单实例、内测角色、边界值全部待报批。
recorded_at: 2026-09-19T12:00:00+08:00
updated_at: 2026-09-19T12:00:00+08:00
inputs:
  - 我的规划/第六期-AI办公助理Agent产品线.md（总览 v2，里程碑验收／边界值默认值表／异常矩阵基线）
  - 我的规划/第六期-提案-proposal.md（范围与场景 S1-S4）
  - 我的规划/第六期-技术设计-design.md（组件落点／KP 时序／SDK 事件映射／选型，机制基线）
  - 我的规划/第六期-接口规格-spec.md（API／事件／错误码／OQ-1..OQ-8 登记，字段级基线）
  - records/plans/PLAN-PLATFORM-ACCOUNT-MEMBERSHIP.md（计划格式先例）
notes: 边界值一律引用总览「边界值默认值表」12 项待报批默认值，本计划不改任何数值；输入文档冲突以「⚠ 冲突」行登记并采信 spec（字段级）与 design（机制）；存在阻塞未决项，status 保持 proposed，不得置 approved。
---

# AI 办公助理（Agent 产品线）执行计划板

> 输入材料（2026-09-19 全文通读）：总览 v2、提案、技术设计、接口规格四件套（见 frontmatter `inputs`）；格式先例 `records/plans/PLAN-PLATFORM-ACCOUNT-MEMBERSHIP.md`。
> 代码证据（2026-09-19 读取）：`server/internal/authz/catalog.go:82-115`（权限注册表现值 32 个，增 office.read/write 后 34，实现时以注册表实数为准并用动态计数断言）；`server/internal/platform/{identity,billing,membership,storage}/`（复用层在位）；`server/internal/canvas/routes.go`（Mount*Routes 域路由表模板）；`server/internal/service/{cleanup.go,reconcile.go,moderation.go}`（定时任务与 moderation 复用点）；`canvas-agent/`（express + skills store + MCP + winston 先例）；`docker-compose.yml`、`nginx.conf`（接入落点，nginx 已有 `/api/v1/ai/` SSE 模板）；`records/tests/infinite-canvas-feedback-generation-rating/`（用例库命名与 run 记录先例）。
> 基线状态：无任何实现发生——`office-agent/`、`server/internal/office/`、`web/src/pages/office/` 均未创建；本计划全部任务 `☐ 未开始`。本文件是执行契约，未经用户评审通过不进入实施。

## 进展概览

进度：0/18 已完成、0 进行中、0 任务阻塞；范围 = M0 spike + M1 端到端最小链路 + M2 工具与工作区 + M3 模型矩阵与计费 + 测试与评审横切，全部未开始；计划状态 proposed（存在阻塞未决项 OQ-1/2/5/6，未获用户评审与开工授权）。

| 事项 | 业务侧解读 | 技术侧解读 | 交付效果 |
| --- | --- | --- | --- |
| M0 前置 spike | 先花 1–2 天确认技术路线走得通再开工，避免中途推翻 | LiteLLM 配置 + GLM 直通冒烟；office-agent 最小 query() 实证 resume 续接与 SDK 事件逐类型映射（透传／忽略／转换三选一登记）；bubblewrap 容器内可行；GPT 翻译路径最小试跑；覆盖总览待验证假设清单全部 6 项 | spike 结论记录落档；G0 评审门：任一假设证伪回炉设计，不进 M1 |
| M1 端到端最小链路 | 内测用户能在网页上和 Agent 对话：流式回复、断线不丢消息、能取消 | office-agent 四端点 + 事件归一化；Go office 域 8 表迁移、串行闸、SSE 转发／Last-Event-ID 重放、cancel、孤儿回收、消息物化；web 最小聊天页；compose/nginx 接入 | 浏览器端到端可用：流式回复、刷新历史完整无重复、杀 Runtime 后已落库事件不丢且 TTL 后 failed、cancel ≤2s（待报批）、并发 409、`office_events` seq 完整 |
| M2 工具与工作区 | Agent 能真的动手干活：读写文件、跑命令、搜资料，产出可预览可下载的成果 | 文件工具（Read/Write/Bash）+ 每会话工作区；tool_call/tool_result 过程卡片；附件上传注入；Artifacts 落库与净化预览／下载；自建搜索工具（OQ-8 选型前置） | 「写一份 markdown 调研报告」端到端：过程卡片可见、artifact 可预览下载、含 `<script>` 的 html 预览不执行、工作区跨 run 持久 |
| M3 模型矩阵与计费 | 多模型可选、按点数扣费、后台可管、仅内测可用 | Kimi/MiniMax 直通 + GPT 翻译评测门禁；能力位路由表与前端过滤；计费闭环（预检 + run 中检测 + 终态事务扣减 + cost 对账）；admin 智能体／技能／路由管理页；office.read/write 接入内测角色 | 同会话切模型续聊、点数不足收 `quota_exhausted` 并终止、admin 改配置对新建 run 即时生效、GPT 达标判定明确、cost 对账偏差 ≤5% 或启用自算口径 |
| 横切测试与评审 | 每一期都有人独立验过才算数，作者不自审 | test-planner 产出用例库 → test-executor 每期执行 API／边界用例并编译 UI 步骤脚本 → 独立 reviewer 门；浏览器自动化由主会话 supervisor 执行（AGENTS.md 分工） | `records/tests/infinite-canvas-office-agent/` 用例库与各期冒烟集全过，G1–G3 关闭有据 |

- 进展回写纪律：执行期由 Supervisor 唯一写任务表的「状态」与「证据回写」两列；任务完成只回写该行此两列（最小 diff），不重写已完成行；未决项只改其状态列；`plan_version` 与 `updated_at` 随计划修订刷新。
- 我们在哪：计划 v1（proposed）；用户已口头批准开工（「开始实现一版…先跑通」）。M0 网关冒烟 + M1 核心链路已在本地三进程跑通：office-agent（8902，Claude Agent SDK 0.1.77 + glm-5.3 经 new-api 网关）、Go（8093，SQLite 开发库 + 种子账号）、vite（3001，/api 代理已切 8093——验收完改回 8091 即还原）。T01/T03/T04/T05/T07 进行中；T06 compose/nginx 与 T02 用例库未开工；浏览器走查与 G1 评审门待做。
- 下一步：① 用户评审四件套与边界值表（OQ-1/2/5 需拍板，OQ-3/4 可后置至对应期前）→ ② 开工授权后按 AGENTS.md 先确认分支（建议 `feature-office-agent`）再动手 → ③ T01 spike → G0 评审 → ④ M1（T03–T07）→ G1 → ⑤ M2（T08–T13）→ G2 → ⑥ M3（T14–T18）→ G3。
- 阻塞风险：OQ-6 SDK 假设（resume/fork、canUseTool、compact、bubblewrap、LiteLLM cost 精度、GPT 保真度）任一证伪即回炉；OQ-5 边界值未报批前实现一律引用配置变量不硬编码；GPT 翻译工具调用保真度以评测门禁兜底（不达标限场景或延后）；容器即沙箱只防事故不防恶意用户，公众开放前必须完成沙箱阶段二（验收红线）；MySQL 特有迁移坑（列长度／保留字）以真实库迁移一次兜底（仓库红线）；M1–M3 单实例为明确收缩，可用性靠孤儿回收 + failed 可重试兜底。

**⚠ 冲突登记**（采信规则：字段级以 spec 为准，机制以 design 为准，不自行发明新设计）：

- ⚠ 冲突-1：katex 期次——总览「前端规划」写「katex 公式预留」，design §2.6 选型表将 katex／rehype-katex 列为 M1。采信 design：T05 渲染管线 M1 一步到位（react-markdown + remark-gfm + rehype-sanitize + hljs + katex）。
- ⚠ 冲突-2：M1 计费范围——总览「M1 内容」清单未列计费，但总览 KP-1（标注 M1 核心链路）步 7 含「计费扣减流水（唯一键 run_id）」、E5 标注「M1：余额 ≤0」、spec §2.2 done payload 含 credits。采信 design §6 与 spec：M1 落预检（余额 ≤0 拒绝）+ 终态扣减同事务 + E9 重试兜底；M3 落预估折点、run 中检测（usage 30s）、`quota_exhausted`、cost 对账（T16）。

## 任务清单

| 任务ID | 内容 | 优先级 | 代码落点 | 依赖 | 状态 | 证据回写 |
| --- | --- | --- | --- | --- | --- | --- |
| T01 | 【M0】spike 假设验证：① LiteLLM 配置 + GLM 直通 curl 冒烟；② office-agent 最小骨架 query() 两轮对话，实证 resume 续接（第二轮能引用第一轮上下文）与 SDK 全部 message/block 类型逐类型映射（透传／忽略／转换三选一显式登记，产出事件映射完备清单，映射完备性为出口条件）；③ bubblewrap 容器内可行性；④ GPT 经 LiteLLM 翻译路径最小工具调用试跑；⑤ 覆盖总览待验证假设清单全部 6 项（resume/fork、canUseTool、compact 质量、bubblewrap、LiteLLM cost 精度、GPT 工具保真度）；产出 spike 结论记录（含事件映射表与失败项）。出口 = G0：任一证伪回炉对应设计章节再评审，不进 M1 | P0 | office-agent/（最小骨架）、docker-compose.yml（LiteLLM 服务）、records/plans/PLAN-OFFICE-AGENT-m0-spike.md（结论记录，比照 parity-inventory 先例） | — | ◐ 进行中 | 网关冒烟通过：newapi.epsq.cn Anthropic 协议非流式+流式（glm-5.3→flash，默认开思考，已验证 Bearer/x-api-key 双鉴权）；resume 跨 run 实证（第二轮正确复述上一轮）。LiteLLM 未引入——M1 直连用户自有 new-api 网关（更短链路，偏差记 spec 待回填）；bubblewrap/GPT 翻译/compact 质量三项未测 |
| T02 | 【横切·test-planner】测试设计：产出用例库 `records/tests/infinite-canvas-office-agent/`（契约 `records/tests/README.md` 约定，命名比照 infinite-canvas-feedback-generation-rating 先例）：契约一 A1–A12、契约二 B1–B4、spec §7 判定规则 5 条、§8 边界场景 8 类、异常矩阵 E1–E14、权限点动态计数；测试替身表面清单（fake office-agent／fake Anthropic 上游，要求见「接口与共享机制」）；M1/M2/M3 各期冒烟集供「验证命令」引用 | P1 | records/tests/infinite-canvas-office-agent/ | — | ☐ 未开始 | — |
| T03 | 【M1】office-agent 骨架：express 四端点 B1 POST /v1/runs（响应 SSE 归一化事件流）、B2 POST /v1/runs/:id/cancel（runId 幂等，未知返回 200）、B3 GET /v1/runs/:id（E13 对账）、B4 GET /healthz；X-Office-Internal-Token 校验（crypto.timingSafeEqual）；SDK 事件归一化（按 T01 映射表，zod schema 与 Go 侧一一对应）；sessionId 推导工作区 + 正则强校验（不匹配 400 invalid_session_id，不接受外部路径）；resume-per-turn transcript 落盘 `../transcripts/{sessionId}.jsonl`；SDK 版本锁定；winston 日志带 run_id/session_id；独立 Dockerfile（照 canvas-agent） | P0 | office-agent/src/{index.ts,auth.ts,run/,events/,workspace.ts}、office-agent/Dockerfile | T01 | ◐ 进行中 | 四端点+密钥校验+事件归一化+resume 落地（Claude Agent SDK 0.1.77 锁定）；真实 glm-5.3 E2E（run_started→29 delta→done 109/52 tokens）、cancel、resume、跨重启续接全过；偏差：transcript 用 .json 非 .jsonl、日志未接 winston、Dockerfile 未建（归 T06） |
| T04 | 【M1】Go office 域：按 spec §4 一次迁移建全 8 张 office_* 表 + 种子默认智能体一行（A1 缺省用）；A1–A7 七端点 + MountOfficeRoutes + main.go 注入；注册 office.read/office.write 权限点（注册表 32→34，动态计数断言，默认不授予普通用户）并用于端点鉴权；串行闸（事务内 SELECT FOR UPDATE 会话行 + active_run_id 可空唯一索引，冲突 409 run_conflict）；事件落库分配 seq（UNIQUE(run_id, seq)）+ SSE 逐事件转发 + Last-Event-ID 重放 + 410 events_expired；cancel（幂等，转发超时 E12）；计费 M1 范围（预检余额 ≤0 拒绝经 platform/billing + 终态事务：置终态 + 清 active_run_id + 物化 assistant 消息（threadId/turnId/itemId）+ 扣减流水 UNIQUE(run_id)，E9 系统重试定时任务）；孤儿回收扫描（E13，启动 + 周期，TTL 待报批）；office_events 保留期清理任务（默认 7 天，待报批）；⚠ 冲突-2 采信结论在本次落地 | P0 | server/internal/office/（handler/service/runtime/events/billing/model/routes）、server/internal/model/、server/internal/db/db.go、server/cmd/server/main.go、server/internal/service/（E9 重试与清理任务）、server/internal/authz/catalog.go | T01 | ◐ 进行中 | 5 表双方言迁移 + 真库（9.3.0 容器）逐表核对 + 服务器 MySQL 8.4.11 部署实测（49 表全量迁移成功）；A1–A9 + 编排（clientMsgId 幂等/409 串行闸/seq 落库重编/快照物化/credits_charged 扣费闸/孤儿扫描）11 测试全绿 + 全仓 go test 绿；E2E 实测（SSE 32 事件、cancel 全链路）；偏差：权限点 32→34 已落（office.read/write，IsSystem 消费点全量核对 + support 角色 403 实证）、8 表全量、E9 定时补偿、保留期清理按 M1 裁决后置；另修：run_started 回写 model（统计依赖）+ 扣费微元粒度修复（整数除法吞零） |
| T05 | 【M1】web 最小聊天页 /office（落位按 OQ-2 默认现有 web，发布前需用户确认）：路由接入 router.tsx；布局 = 会话列表 + 对话流 + done/error；发消息带 clientMsgId（uuid，幂等）与 cancel；页面私有 hook use-office-chat.ts（EventSource 自动带 Last-Event-ID 重连、410 events_expired 降级快照重建、快照权威合并：已物化 turn 重放只恢复过程卡片不追加文本）；渲染管线 react-markdown + remark-gfm + rehype-sanitize 白名单净化 + highlight.js + katex（⚠ 冲突-1 采信 design M1 一步到位）；主题走 canvasThemes/全局 token，不硬编码颜色；运行中消息不做 localStorage 持久化 | P1 | web/src/pages/office/{index.tsx,use-office-chat.ts,components/}、web/src/router.tsx | T04 | ◐ 进行中 | 路由+三栏工作台+流式卡+重连+410 降级落地；sse.ts 6 单测（半包/粘包/跨 chunk/401/410）、tsc 0 错、16 vitest 全绿；偏差：渲染管线用仓库既有 Streamdown（自带净化/高亮/公式）替代 react-markdown 栈；浏览器走查待用户 |
| T06 | 【M1】compose/nginx 接入：docker-compose.yml 增 office-agent 与 LiteLLM 两服务（仅内网可达、不映射宿主端口、不经理 nginx；X-Office-Internal-Token 部署时随机生成注入两侧 env，模型 key 仅 LiteLLM env）；工作区与 transcript 持久卷挂载；nginx.conf 增 `/api/v1/office/` location（SSE、buffering off，600s 模板照 `/api/v1/ai/`）；LiteLLM 路由表初版（GLM 直通，能力位随 T14 补全；OQ-1 拍板前模型名引用配置变量） | P1 | docker-compose.yml、nginx.conf、部署 env 模板 | T03, T04 | ☐ 未开始 | — |
| T07 | 【M1】M1 验收走查 + 评审门 G1：test-executor 执行 T02 的 M1 冒烟集（API／边界用例）并编译 UI 步骤脚本；真实 MySQL 迁移启动一次（8 张新表，仓库红线，SQLite 不能替代）；主会话浏览器端到端对照总览 M1 验收清单逐条（流式回复／刷新历史完整无重复／杀 Runtime 后重连不丢已落库事件且 TTL 扫描后 failed（E13/E14 实测）／cancel 点击到收尾 ≤2s（待报批）／office_events 完整 seq／同会话并发提交 409）；独立 reviewer 门（agentflow:reviewer，作者不得自审）+ 权限点新增过 technical-director（AGENTS.md 硬规则） | P0 | 无新代码；证据回写 records/tests/infinite-canvas-office-agent/run-<日期>-<序号>.md | T02, T03, T04, T05, T06 | ◐ 进行中 | curl 级 E2E 全链路通过（登录→建会话→发消息→SSE→done→快照物化→cancel→resume→管理员充值→计费闸/扣费）；真库迁移核对 9.3.0（8.4.11 复核待办，OFFICE_MYSQL_DSN 门控用例已备）；T02 用例库未产出、浏览器走查与 reviewer 门未执行 |
| T08 | 【M2】文件工具与工作区：Read/Write/Bash 工具限制在会话工作区目录（bubblewrap 按 T01 结论启用，不可行则降级并 notice 事件告知）；MCP 内置工具静态注册（@modelcontextprotocol/sdk，canvas-agent 模式）；工作区文件跨 run 持久（resume-per-turn 生效）验证 | P0 | office-agent/src/{run/,mcp/,workspace.ts} | T07 | ☐ 未开始 | — |
| T09 | 【M2】工具过程卡片 UI：tool_call/tool_result 事件渲染过程卡片（toolCallId、name、inputPreview/outputPreview、isError、status）+ notice 提示渲染；已物化 turn 的重放事件只恢复过程卡片不改写文本（spec §7-5 物化不可逆）；OQ-3 若拍板提前 AskUserQuestion，则在本任务追加 clarification 交互 | P1 | web/src/pages/office/components/ | T07 | ☐ 未开始 | — |
| T10 | 【M2】附件上传注入：A11 两端点（POST /files/upload-url、POST /files/complete）包装现有 storage 域 presigned；office_files 落库；B1 附件清单（key/mime/name）随 run 注入工作区供 Agent 读取；前端上传组件（大小沿用 storage 现有限值，口径待报批）；不存在 attachmentId／agentId 返回 400 invalid_param（spec §8） | P1 | server/internal/office/（A11）、web/src/pages/office/、office-agent/src/run/ | T07 | ☐ 未开始 | — |
| T11 | 【M2】Artifacts 落库／预览／下载：Runtime 识别工作区产出并上报 artifact 事件（按扩展名／内容分类 markdown/code/html/file）；Go 落 office_artifacts；A8 列表、A9 详情（html 返回源码视图标记）、A10 下载（Content-Disposition: attachment + 签名 URL 跳转）；预览渲染净化白名单（rehype-sanitize，与对话流共用管线）；前端 artifact 抽屉与卡片（代码/JSON 类用 Monaco 只读；html 类型不渲染富内容）；净化实测进验收 | P0 | office-agent/src/（events／run 的 artifact 识别）、server/internal/office/（A8–A10）、web/src/pages/office/components/ | T07 | ☐ 未开始 | — |
| T12 | 【M2】自建搜索工具：任务内前置检查 = OQ-8 上游选型定稿（不设独立任务，开工前完成）；接第三方搜索 API 注册为 Runtime 工具（替代 Anthropic 服务端 web_search/web_fetch，第三方端点均无）；上游不可用时 notice 降级说明；支撑 S3 调研场景 | P1 | office-agent/src/mcp/ | T08 | ☐ 未开始 | — |
| T13 | 【M2】M2 验收走查 + 评审门 G2：test-executor 执行 M2 冒烟集；主会话浏览器端到端对照总览 M2 验收清单（「写一份 markdown 调研报告」→ 过程卡片可见 → artifact 可预览可下载；工作区文件跨 run 持久；附件能被 Agent 读取；含 `<script>` 的 html artifact 预览不执行——净化实测断言）；独立 reviewer 门（作者不得自审）；OQ-3 范围核对 | P0 | 无新代码；证据回写 records/tests/infinite-canvas-office-agent/run-<日期>-<序号>.md | T02, T08, T09, T10, T11, T12 | ☐ 未开始 | — |
| T14 | 【M3】模型矩阵与能力位：Kimi/MiniMax 直通路由 + GPT 翻译路由就绪（评测门禁在 T15）；能力位实体（vision、context_tier、thinking、server_tools）进 Go 配置；A12 GET /agents、GET /skills（能力位随 agent 返回）；前端按能力位过滤可选模型（不让用户选跑不动的组合）；OQ-1 首发模型组合在本任务落定路由表 | P0 | docker-compose.yml（LiteLLM 路由表）、server/internal/office/（能力位 + A12）、web/src/pages/office/ | T07 | ☐ 未开始 | — |
| T15 | 【M3】GPT 翻译评测门禁：前置 = OQ-7 评测任务集清单定稿（默认建议 20 题典型办公任务：10 文档 + 6 表格/数据 + 4 代码，随边界值一并报批）；在固定任务集上评测，工具调用成功率 ≥90% 且无致命失败（达标线待报批）；产出评测报告与达标判定；不达标 → GPT 限非工具场景或延后（决策回写证据列），国产三家不受影响 | P1 | 评测脚本与报告（证据回写 records/tests/infinite-canvas-office-agent/）、office-agent/（必要时适配） | T14 | ☐ 未开始 | — |
| T16 | 【M3】计费闭环收口：M3 预检增强（按 agent 最大输出上限折算预估点数，超余额 400 credits_exhausted）；usage 事件周期发送（默认 30s 节流，待报批）+ Go 累计折点 + 超余额走 KP-3 主动取消并发 quota_exhausted（随后 error 收尾）；失败 run 已耗 token 照实计费（以 Runtime 上报累计 usage 为准，崩溃窗口丢失不追补不收取）；LiteLLM cost 对账门禁：抽样 run 覆盖直通与翻译两条路径（翻译路由 T14 就绪），cost 与自算值偏差 ≤5% 不达标则永久采用自算口径（官方单价表配置化）；以上数值全部待报批 | P0 | server/internal/office/billing.go、server/internal/service/（对账／重试任务，照 reconcile.go 模式）、server/internal/platform/billing/（仅复用不改语义） | T07, T14 | ☐ 未开始 | — |
| T17 | 【M3】admin 管理页与权限接入：admin 智能体／技能／模型路由管理（office_agents/office_skills/能力位 CRUD；改配置对新建 run 即时生效，进行中 run 不中断，作用域仅此）；office.read/office.write 绑定新建内测角色（admin 手动绑定用户，默认不授予普通用户——权限回收即第一道下线开关；admin 端点鉴权复用 office.read/write，权限点总量维持 34 不新增）；内容审核接入：用户输入经现有 service 层 moderation 审查，artifact 审核规则落地（至少存储侧标记 + admin 可见）；OQ-4 若拍板并入，则接现有 generation_feedbacks 基建 | P1 | admin/src/、server/internal/admin/、server/internal/authz/（角色绑定）、server/internal/office/service.go（moderation 接入） | T14, T16 | ◐ 进行中 | admin 接口三端点（/api/admin/office/sessions+detail+stats）与 admin「办公助理」页（会话查看/统计看板）已落地并在服务器部署验证；office.read/write 权限点 32→34 已注册（IsSystem 核对通过、support 角色隔离 403 实证）；未做：内测角色绑定、moderation 接入、OQ-4 反馈基建、office_agents/skills 管理页（M3 其余部分） |
| T18 | 【M3】M3 验收走查 + 评审门 G3：test-executor 执行 M3 冒烟集；主会话浏览器端到端对照总览 M3 验收清单（同会话切换模型续聊／点数不足收到 quota_exhausted 并终止／admin 改配置对新建 run 即时生效／GPT 评测报告达标判定明确／cost 对账偏差 ≤5% 或启用自算）+ proposal 产品标准 S1–S4 四场景脚本走查（内测用户独立完成、无开发介入）；独立 reviewer 门 + 计费语义过 technical-director（AGENTS.md 硬规则） | P0 | 无新代码；证据回写 records/tests/infinite-canvas-office-agent/run-<日期>-<序号>.md | T02, T13, T14, T15, T16, T17 | ☐ 未开始 | — |

### 未决项

> 摘自 spec §9 未决问题登记（OQ-1..OQ-8），阻塞对象映射到本计划任务。存在阻塞未决项（OQ-1/2/5/6），故本计划 status=proposed，全部关闭并经用户评审前不得置 approved。

| 编号 | 问题 | 是否阻塞 | 阻塞对象（本计划任务映射） | 状态（spec §9 原文） | 关闭方式 |
| --- | --- | --- | --- | --- | --- |
| OQ-1 | 首发模型组合（总览决策项 2） | 是 | M1 模型路由配置（T04/T06 引用变量不硬编码）与 M3 首发组合落定（T14） | 待用户 | 用户拍板（建议 glm-5.3 主力 + kimi-k2.7-code 编码 + MiniMax-M3 多模态，GPT 评后定） |
| OQ-2 | /office 落位（总览决策项 3） | 是（可按默认先行） | 前端聊天页 T05（spec 原文阻塞对象为「前端任务 T-T04」） | 待用户（默认现有 web） | 用户确认；T05 按默认落位开发，发布验收前必须确认 |
| OQ-3 | AskUserQuestion 是否提前 M2（总览决策项 6） | 否（不阻塞 M1；阻塞 M2 范围定稿） | T09 是否追加 clarification 交互，T13 范围核对 | 待用户 | 用户拍板（评审建议提前，成本低） |
| OQ-4 | 生成反馈并入 M3（总览决策项 7） | 否（不阻塞 M1/M2；阻塞 M3 范围定稿） | T17 是否接 generation_feedbacks，T18 范围核对 | 待用户 | 用户拍板（建议并入，复用成本较低） |
| OQ-5 | 边界值默认值表整体报批（总览决策项 8，12 项） | 是 | 全部边界值冻结；报批前实现一律引用 spec §6 变量不硬编码，不得进入发布／验收 | 待用户 | 用户按总览「边界值默认值表」逐项或整体报批 |
| OQ-6 | SDK resume/事件映射/bubblewrap 假设（M0 spike 出口） | 是 | M1 开工（T03/T04 依赖 T01 通过 G0） | M0 验证 | T01 spike 实证 + G0 评审（supervisor + 用户确认），证伪回炉 |
| OQ-7 | GPT 评测任务集清单细化（20 题构成） | 是 | M3 门禁执行（T15） | M3 前定 | T15 开工前定稿并随边界值报批 |
| OQ-8 | 自建搜索工具的上游选型 | 是 | M2 T12（任务内前置检查，不设独立任务） | M2 前定 | T12 开工前选型定稿并登记证据 |

## 接口与共享机制

### 契约一（前端 ↔ Go）：`/api/v1/office/**`

字段级语义冻结于 spec §1–§2；错误形状沿用平台 `{"code","message","detail"}`；分页 `?cursor=&limit=`（默认 20，最大 100）；幂等键 `clientMsgId`（uuid v4）。

| 端点 | 语义摘要 | 注册点（代码落点） | 引入任务 |
| --- | --- | --- | --- |
| A1 POST /sessions | 创建会话（agentId? 缺省用默认智能体） | server/internal/office/handler.go（MountOfficeRoutes） | T04 |
| A2 GET /sessions | 会话列表（updated_at 倒序，cursor 分页） | 同上 | T04 |
| A3 GET /sessions/:id | 会话详情（含 activeRunId） | 同上 | T04 |
| A4 GET /sessions/:id/messages | 消息快照（权威；?afterTurnId= 增量） | 同上 | T04 |
| A5 POST /sessions/:id/messages | 发消息起 run（201 queued；幂等重放 200；409 run_conflict；400 credits_exhausted；429 rate_limited） | 同上 | T04 |
| A6 GET /sessions/:id/runs/:runId/stream | SSE 事件流（Last-Event-ID 重放；410 events_expired 走快照） | 同上 | T04 |
| A7 POST /runs/:id/cancel | 取消（幂等；非 queued/running 返回 200） | 同上 | T04 |
| A8 GET /sessions/:id/artifacts | artifact 列表 | 同上 | T11 |
| A9 GET /artifacts/:id | artifact 详情（html 返回源码视图标记） | 同上 | T11 |
| A10 GET /artifacts/:id/download | 下载（Content-Disposition: attachment + 签名 URL） | 同上 | T11 |
| A11 POST /files/upload-url ／ POST /files/complete | 附件 presigned 上传（storage 域包装） | server/internal/office/ + server/internal/storage/ | T10 |
| A12 GET /agents ／ GET /skills | 可用智能体/技能列表（能力位随 agent 返回） | server/internal/office/handler.go | T14 |

### 契约二（Go ↔ office-agent，内网）：`/v1/**`

认证：所有请求带头 `X-Office-Internal-Token`（部署时随机生成注入两侧 env，缺失/不匹配 401，Go 按 E3/E4 处理）；office-agent 不映射宿主端口、仅 compose 内网可达。字段级语义冻结于 spec §3。

| 端点 | 语义摘要 | 注册点（代码落点） | 引入任务 |
| --- | --- | --- | --- |
| B1 POST /v1/runs | 起 run；body：runId、sessionId、message（role/content/attachments）、agent（systemPrompt/model/tools/skills）；响应 SSE 归一化事件流（seq 由 Runtime 临时编号，Go 落库时重编） | office-agent/src/index.ts；消费方 server/internal/office/runtime.go | T03（T04 消费） |
| B2 POST /v1/runs/:id/cancel | 取消；未知 runId 返回 200（幂等） | office-agent/src/index.ts | T03 |
| B3 GET /v1/runs/:id | 存活与状态查询 `{exists, phase, sessionId}`（E13 对账用） | office-agent/src/index.ts | T03 |
| B4 GET /healthz | `{ok, sdkVersion}` | office-agent/src/index.ts | T03 |

### 共享机制（跨组件约定）

| 机制 | 约定 | 注册点 |
| --- | --- | --- |
| 事件信封 v/seq | 统一信封 `{"v":1,"seq","type","runId","ts","payload"}`；seq 由 Go 落库时分配，UNIQUE(run_id, seq)；Last-Event-ID 按 seq 重放；未知 type 必须忽略（向前兼容）；协议版本 v 与消息存储版本独立管理 | server/internal/office/events.go ↔ office-agent/src/events/（zod schema 一一对应），T03/T04 |
| 串行闸 | 同会话强制串行：事务内 SELECT FOR UPDATE 会话行校验 active_run_id（可空唯一索引），存在活动 run 返回 409；M1–M3 单实例部署声明 | server/internal/office/service.go，T04 |
| 终态唯一写者 | run 状态机 queued→running→succeeded/failed/cancelled 仅 Go 可写；queued→running 由 Go 收首个 run_started 迁移；Runtime/前端对终态的认知仅是缓存 | server/internal/office/service.go，T04 |
| 快照权威与物化 | office_messages 是历史唯一权威；归属键 thread_id=session_id、turn_id=run_id、id=itemId（仓库 agent 消息硬规则）；物化不可逆，重放事件不得改写已物化文本 | server/internal/office/service.go，T04 |
| resume-per-turn | 每 run 结束 transcript 落持久卷，下个 run resume 续上下文；不做常驻交互进程 | office-agent/src/run/，T03 |
| 工作区路径服务端推导 | `/data/workspaces/{sessionId}/`，正则 `^[a-z0-9][a-z0-9-]{17,39}$` 强校验；Runtime 不接受任何外部传入路径 | office-agent/src/workspace.ts，T03 |
| 契约二认证 | 共享密钥头 X-Office-Internal-Token，timingSafeEqual 常量时间比较 | office-agent/src/auth.ts + server/internal/office/runtime.go，T03/T04/T06 |
| 权限点 | office.read / office.write（注册表 32→34，实现时以 server/internal/authz/catalog.go 实数为准、测试动态计数断言）；默认不授予普通用户，内测角色绑定在 T17 | server/internal/authz/catalog.go，T04 注册／T17 角色绑定 |
| 计费复用 | 预检与扣减一律经 platform/billing（服务构造绑定 product），流水 UNIQUE(run_id) 幂等，不跨产品域 import | server/internal/office/billing.go → server/internal/platform/billing/service.go，T04（M1 范围）／T16（闭环） |
| moderation 复用 | 用户输入经现有 service 层 moderation 审查；artifact 审核规则 M3 定义（存储侧标记 + admin 可见） | server/internal/service/moderation.go 消费于 server/internal/office/service.go，T17 |
| 能力位路由表 | 每模型带 vision／context_tier／thinking／server_tools 能力位；前端按位过滤可选模型 | LiteLLM 路由表（compose 配置）+ server/internal/office/ 能力位实体，T06 初版／T14 全量 |
| 边界值配置变量 | spec §6 全部 12 项待报批默认值实现一律引用配置变量，不硬编码；数值调整只改配置与总览表，不改逻辑分支 | server/internal/config/config.go、office-agent/src/config.ts，T03/T04 |
| 存储版本独立管理 | office_messages.content 等结构化 JSON 携带 schema_version；存储格式升级先备份再迁移，遇未知版本/损坏拒绝覆盖 | server/internal/model/ + server/internal/office/，T04 |
| 渲染净化白名单 | 对话流与 artifact 预览共用管线：markdown 解析 → rehype-sanitize 白名单必经层 → hljs/katex；html artifact 仅源码视图；下载 attachment 强制 | web/src/pages/office/ + server/internal/office/（A9/A10），T05/T11 |
| 日志贯穿 | run_id/session_id/user_id 贯穿四跳（web→Go→Runtime→LiteLLM），LiteLLM 以 request 头透传 run_id；告警指标（runtime_unreachable/stalled/orphan_reclaimed/billing_fail/cancel_timeout）落 admin 面板可见 + 日志阈值 | Go 现有结构化日志 + office-agent winston，T03/T04 |

### 关键路径时序基线（引用，不自造第二套）

| 路径 | 基线 | 失败分支 |
| --- | --- | --- |
| KP-1 发消息起 run（M1 核心） | 总览/design「关键路径时序」KP-1 步骤表（8 步：幂等提交→FOR UPDATE 串行检查→内网起跑→run_started 迁移→逐事件落库转发→终态事务{终态+清 active_run_id+物化+扣减}→快照权威合并）；步骤表为基线，时序图仅作伴图，不一致以表为准 | E1/E2/E3/E4/E5/E6/E7/E8/E9/E10 |
| KP-2 断线重连与重放 | 同上 KP-2（Last-Event-ID 重放；410 空洞走快照重建） | E7/E10/E11 |
| KP-3 取消 | 同上 KP-3（终态幂等 200；running 转发 Runtime cancel；点击到收尾 ≤2s 待报批） | E12 |

### 异常矩阵（E1–E14，执行索引）

> 权威来源：总览／design「异常矩阵」全量字段 + spec §5 错误码总表（冻结）+ spec §7 判定规则（冻结）。本表为执行索引，浓缩自权威来源；超时等数值均为待报批默认值。

| 编号 | 失败模式 | 检测与处置 | 重试（主体/次数/退避） | 幂等前提 | 关联任务 |
| --- | --- | --- | --- | --- | --- |
| E1 | 同 clientMsgId 重复提交 | UNIQUE(session_id, client_msg_id) 冲突 → 返回既有 run 与事件流 | N/A（幂等命中即成功） | clientMsgId | T04 |
| E2 | 同会话并发提交 | active_run_id 非空 → 409 run_conflict | 用户手动（等当前 run 结束） | — | T04 |
| E3 | Go→Runtime 起跑超时 | HTTP 超时（默认 10s）→ run failed(runtime_unreachable)，清 active_run_id | 用户手动 | clientMsgId | T04/T06 |
| E4 | Runtime 崩溃/断连 | 契约二 SSE 断开 → failed(runtime_lost)，孤儿 TTL 兜底 | 用户手动 | clientMsgId | T04 |
| E5 | 计费预检不过 | 余额 ≤0（M1）/预估超余额（M3）→ 400 credits_exhausted，不建 run | 用户充值 | — | T04/T16 |
| E6 | 事件静默（模型停摆） | 超阈值（默认 120s）无任何事件 → Go 主动 cancel，failed(stalled) | 用户手动 | clientMsgId | T04 |
| E7 | Go→前端转发中断 | 客户端断开 → run 照常执行落库，重连走 KP-2 | 客户端自动重连 | Last-Event-ID | T04/T05 |
| E8 | LiteLLM/上游超时 | Runtime 请求超时（默认 120s）→ error(upstream_timeout) 收尾 | 用户手动/换模型 | clientMsgId | T03 |
| E9 | 扣减落库失败 | 终态事务报错 → 终态照落，扣减进定时任务重试 | 系统 5 次×60s 退避（待报批），仍失败转人工 | 流水 UNIQUE(run_id) | T04/T16 |
| E10 | 重放与快照重复合并 | turnId 已物化判定 → 快照权威，事件仅补未物化 turn | — | turnId/itemId | T04/T05 |
| E11 | 重放命中已清理区间 | seq < 清理水位 → 410 events_expired + 终态事件，前端走快照 | N/A | — | T04/T05 |
| E12 | Runtime cancel 转发超时 | 转发超时（默认 5s）→ Go 强制置 cancelled，Runtime 孤儿 TTL 兜底 | N/A | cancel 幂等 | T04 |
| E13 | Go 崩溃重启 | 启动扫描 queued/running 且 updated_at 超 TTL（默认 10min，扫描间隔 1min）→ GET /v1/runs/:id 对账；无记录 → failed(orphan_reclaimed)；仍在跑 → 强制 cancel+failed（M1 简化取舍，续转留 M4+） | N/A（系统回收） | 扫描按 runId 幂等 | T04 |
| E14 | Runtime 自身重启 | 启动自检与 Go 对账 → 无状态重建，run 一律由 Go 侧 TTL/查询判定 | 同 E13 | — | T03/T04 |

### 测试替身（stand-in）表面要求

验证依赖以下替身（实现随 T02 测试设计定稿，落点遵守 testutil 禁 import 业务域规则——跨域夹具放使用方域包测试文件内，允许少量重复）：

| 替身 | 必需表面 | 服务对象 |
| --- | --- | --- |
| fake office-agent（Go 域测试 harness） | B1–B4 全端点；SSE 归一化事件流可编程（run_started/delta/done/error/tool_call/tool_result/artifact/usage 样本，按 T01 映射表 fixture）；错误路径：401 无头/错头、起跑挂起（E3）、SSE 中途断开（E4）、静默不发事件（E6）、事件乱序（spec §8）、B2 cancel 幂等（含未知 runId） | server/internal/office/ 契约一/二用例（T04/T07） |
| fake Anthropic 上游（office-agent 测试） | /v1/messages 流式 SSE（message_start/content_block_delta text_delta/tool_use 块/message_delta usage/message_stop、result）；错误路径：超时（E8）、错误响应；用于归一化映射逐类型用例与 resume 续接用例 | office-agent/ 单测（T03/T08） |
| 浏览器端到端 | 非替身：主会话 supervisor 执行（Browser Use 不可委托，AGENTS.md 分工）；test-executor 编译 UI 步骤脚本 | T07/T13/T18，在对应评审门关闭前完成 |

### 发布顺序 × 回滚点

| 步骤 | 内容 | 回滚点（按下线顺序） |
| --- | --- | --- |
| S1 | M1 端到端最小链路（T03–T07），发布形态 = 内测角色可见 /office 入口 | ① 前端入口 feature flag 隐藏 → ② nginx 摘 `/api/v1/office/` location → ③ compose 停 office-agent/LiteLLM → ④ 表保留不删（数据可追溯） |
| S2 | M2 工具与工作区（T08–T13），发布形态 = 同 S1 + 工具能力 | ① office_agents.tools 置空（禁工具执行）→ ② 同 S1 ①–④ |
| S3 | M3 模型矩阵与计费（T14–T18），发布形态 = 同 S1 + 多模型与计费 | ① office.read/office.write 从内测角色回收（API 全量 403）→ ② 同 S1 ①–④；计费异常单独回滚 = 关闭 office 计费开关（走 platform/billing 配置），已扣流水不回冲、人工对账 |

每个回滚点前置条件：该步已通过真实 MySQL 迁移启动一次与 `go test ./...` 全绿；发布顺序固定 S1→S2→S3，不并行上线。

### 评审门

| 评审门 | 时机 | 评审人 | 审什么 |
| --- | --- | --- | --- |
| G0（M0 出口） | T01 完成后、T03/T04 开工前 | supervisor + 用户确认 | 四项 spike 结论（LiteLLM+GLM 冒烟、resume 续接与 SDK 事件映射逐类型实证、bubblewrap 容器内可行性、GPT 翻译最小试跑）+ 待验证假设 6 项全量；任一证伪 → 回炉对应设计章节再评审，不进 M1 |
| G1（M1 出口） | T07 内，端到端用例全过后关闭 | 独立 reviewer（agentflow:reviewer，作者不得自审）+ technical-director（office.read/write 权限点新增，AGENTS.md 硬规则） | 总览 M1 验收清单逐条；T02 M1 冒烟集 + test-executor 执行证据；浏览器端到端（主会话执行）；真实 MySQL 迁移一次；SSE 安全（鉴权/限流/410） |
| G2（M2 出口） | T13 内 | 独立 reviewer | 总览 M2 验收清单（净化实测含 `<script>` 不执行、工作区跨 run 持久、附件可读、artifact 预览下载）；M2 冒烟集证据；浏览器端到端；OQ-3 范围核对 |
| G3（M3 出口） | T18 内 | 独立 reviewer + technical-director（计费闭环语义，AGENTS.md 硬规则） | 总览 M3 验收清单 + S1–S4 四场景走查；GPT 达标判定与 cost 对账结论；M3 冒烟集证据；浏览器端到端；权限回收即下线开关验证 |

评审门纪律：不因实现者自测通过而豁免；每期端到端用例在对应评审门关闭前必须全部通过；评审发现的问题回写任务证据列并复核。

## 验证命令

| 命令/检查 | 预期 |
| --- | --- |
| `cd server && go test ./...` | 全部通过：office 域契约一/二用例（含 fake office-agent harness）、串行 409、Last-Event-ID 重放与 410、孤儿回收 E13、物化去重 E10、E9 扣减重试、权限点动态计数断言 |
| `cd office-agent && bun install && bun test` | 全部通过：事件归一化按 T01 映射逐类型用例、workspace 正则强校验、cancel 幂等、密钥校验、resume 续接用例（fake Anthropic 上游） |
| `cd web && npm run build` | 构建与类型检查通过（/office 页面 + 渲染管线净化） |
| 真实 MySQL 迁移启动一次：`cd server && DATABASE_URL='mysql://<user>:<pass>@127.0.0.1:3306/<db>' go run ./cmd/server`（T07 内执行；S2/S3 若有表结构变更则重复） | 服务启动完成、AutoMigrate 无错、office_* 8 张表落库、种子默认智能体存在；字符串列长度/保留字列名风险只在真库暴露，SQLite 单测不能替代（仓库红线） |
| LiteLLM GLM 直通冒烟（T01/T06）：向 LiteLLM `/v1/messages` 发 Anthropic 格式请求（model=glm-5.3） | SSE 200 流式返回；路由与密钥注入生效 |
| 内网健康检查：compose 网络内 `curl office-agent /healthz`；`docker ps` 核对端口映射 | `{"ok":true,...}`；office-agent 与 LiteLLM 均无宿主端口映射 |
| 用例库冒烟集：`records/tests/infinite-canvas-office-agent/`（T02 产出） | 各里程碑冒烟集全过；端到端用例在 G1/G2/G3 对应评审门关闭前全部通过，评审门不因实现者自测通过而豁免 |
| M1 验收走查（浏览器，主会话执行，对照总览 M1 清单） | 六条全过：流式回复；刷新历史完整且无重复（快照合并生效）；杀 Runtime 后重连不丢已落库事件且 run 在 TTL 扫描后置 failed（E13/E14 实测）；cancel 点击到事件流收尾 ≤2s（待报批）；office_events 有完整 seq；同会话并发提交 409 |
| M2 验收走查（同上，对照总览 M2 清单） | 「写一份 markdown 调研报告」→ 过程卡片可见 → artifact 可预览可下载；工作区文件跨 run 持久；附件能被 Agent 读取；含 `<script>` 的 html artifact 预览不执行（净化实测断言） |
| M3 验收走查 + S1–S4 场景脚本（对照总览 M3 清单 + proposal 成功标准） | 同一会话切换模型续聊；点数不足收到 quota_exhausted 并终止；admin 改配置对新建 run 即时生效（进行中 run 不中断）；GPT 评测报告产出且达标线判定明确（≥90% 待报批）；LiteLLM cost 对账偏差 ≤5% 或启用自算口径；内测用户独立完成 S1–S4 无需开发介入 |
| 权限回归：authz catalog 动态计数测试 + 未授权访问 `/api/v1/office/**` | office.read/office.write 注册后注册表总数 34（以实现时注册表实数为准）；无权限用户 403；内测角色绑定后可访问 |

验收口径总结：各里程碑验收标准唯一来源 = 总览 M0–M3 验收清单 + `records/tests/` 用例库（test-planner 产出），本计划不自造第二套验收数字；全部阈值/上限/保留期为待报批默认值，报批前实现引用 spec §6 变量不硬编码；红线三条——公众开放前完成沙箱阶段二、点数扣减与流水可对账、交付前真实 MySQL 迁移一次。
