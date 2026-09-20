# 第六期提案：AI 办公助理（Agent 产品线）— Proposal

> 文档类型：Proposal/PRD（产品提案与需求）。配套文档：技术设计见 `第六期-技术设计-design.md`，接口规格见 `第六期-接口规格-spec.md`，执行计划见 `records/plans/PLAN-OFFICE-AGENT.md`。总览基线见 `第六期-AI办公助理Agent产品线.md`。
> 状态：待评审。边界值与成本数字均为待报批建议值。

## 一句话提案

在现有 infinite-canvas 平台上新增「AI 办公助理」产品线：用户在网页上与云端 Agent 对话，Agent 在服务端工作区执行工具（读写文件、跑命令、搜索），把结果沉淀为可预览、可下载的成果制品（Artifacts），按点数计费。对标 360「AI 办公助理」与腾讯 WorkBuddy 的已验证产品形态。

## 背景与机会

- 2026-09 对 360 paipai 与 WorkBuddy 的前端考古（证据：agentflow 仓评审记录 REV-20260919-001 及其引用）确认两家均为「Claude Agent SDK 范式」：会话/运行模型、工具过程可视化、SKILL.md 技能、沙箱、Artifacts。产品形态已被市场验证。
- 模型接入路径已验证：智谱/Kimi/MiniMax 均提供官方 Anthropic 兼容端点（协议直通零损耗），GPT 经 LiteLLM 翻译接入——模型不锁定。
- 本平台已具备落地条件（第一期~第五期积累）：账号与权限（authz）、点数计费（platform/billing）、对象存储（storage）、内容审核（service 层 moderation）、SSE 转发经验（nginx + `/api/v1/ai/`）、Node agent 服务先例（`canvas-agent/` 用同构模式包 agent CLI）。缺口只在：Go 侧编排域、Node 侧 Claude Agent SDK Runtime、聊天工作台前端、模型网关。

## 目标用户与场景（落到具体对象与动作）

| 场景 | 用户做什么 | Agent 做什么 | 产物 |
|---|---|---|---|
| S1 周报/文档撰写 | 上传零散笔记/聊天记录截图，说「整理成本周周报」 | 读取附件 → 组织结构 → 写 markdown | markdown 周报 artifact，可预览可下载 |
| S2 数据表格处理 | 上传 CSV，问「按月份汇总销售额并画趋势结论」 | 读文件 → 跑命令统计 → 写结论 | 结论回复 + 结果 CSV/markdown artifact |
| S3 竞品调研 | 给一个主题，说「调研并成文」 | 自建搜索工具检索 → 汇总 → 成文 | 带来源的 markdown 调研报告 artifact |
| S4 代码小活 | 贴一段代码或报错，说「修复并解释」 | 读写工作区代码文件 → 跑命令验证 | 修复后的代码文件 artifact |

每个场景的用户可见物：左侧会话（可回溯）、中间过程（工具卡片实时可视）、右侧产物（artifact 抽屉）。

## 产品原则

1. **过程可视**：Agent 的每一步工具调用对用户可见（对齐对端产品的 tool_call/tool_result 卡片）。
2. **产物可留存**：一切有价值的输出落为 artifact 实体，可预览、可下载、可追溯来源 run。
3. **成本可控**：点数硬闸（预检 + run 中检测 + 终态事务扣减），用户随时知道消耗。
4. **模型可换**：模型是路由表里的一行，产品层不感知厂商差异（能力位过滤除外）。

## 范围界定

### 本期做（M0–M3，逐条可判定）

- M0：LiteLLM+GLM 冒烟、SDK resume/事件映射/bubblewrap/GPT 最小试跑四项 spike，产出结论记录。
- M1：会话创建/列表/发消息（幂等）/流式回复（SSE + Last-Event-ID 重放）/取消（≤2s 判定）/孤儿回收/历史快照；GLM 单模型；内测角色可用。
- M2：文件工具（Read/Write/Bash）+ 每会话工作区；工具过程卡片；附件上传注入；Artifacts 落库/预览（净化）/下载；自建搜索工具。
- M3：Kimi/MiniMax 直通 + GPT 翻译与评测门禁（20 任务集 ≥90% 工具调用成功率）；模型能力位过滤；点数计费闭环；admin 智能体/技能/路由管理；权限点接入内测角色。

### 本期不做（deliberately-not-done，逐条有理由）

| 不做 | 理由 |
|---|---|
| 沙箱容器池+排队 | 内测阶段容器即沙箱够用；协议已预留事件；公众开放是硬前置 |
| 子代理/计划模式 UI/结构化提问交互 | 事件协议预留；非核心闭环；决策项 6 可提前 AskUserQuestion |
| 技能市场与导入 UI | 先 admin 配置；市场是对端增值功能 |
| ASR 语音 / WPS 在线编辑 / 知识库 RAG / 组织团队 | 对端增值功能，与核心闭环无关 |
| Runtime 水平扩展 | M1–M3 单实例声明；扩展需重设计 resume 寻址，M4+ 议题 |
| draft-polish / speed-tiers / unread / session fork / 多端同步 | 对端润色功能，无核心损失 |

## 成功标准（可判定）

| 层 | 标准 |
|---|---|
| M1 技术 | 验收清单全过（见总览文档 M1 节：重放不丢事件、TTL 后 failed、cancel ≤2s、409 并发拒绝等） |
| M2 技术 | 报告场景端到端；含 `<script>` 的 artifact 预览不执行 |
| M3 技术 | GPT 评测达标线判定明确；cost 对账偏差 ≤5% 或启用自算；点数不足收到 `quota_exhausted` |
| 产品 | 内测用户能独立完成 S1–S4 四场景且无需开发介入（M3 结束时用四个场景脚本走查） |
| 红线 | 公众开放前完成沙箱阶段二；点数扣减与流水可对账 |

## 与既有能力的衔接（复用映射）

| 既有能力（期数） | 本期复用方式 |
|---|---|
| 账号/权限（一期） | 登录态、`office.read/write` 权限点、内测角色 |
| 对象存储（二期） | 附件 presigned 上传、artifact 下载签名 URL |
| 点数计费（三期） | 经 platform/billing 扣减，流水唯一键 run_id |
| AI 请求转发与 SSE（四期） | nginx SSE 模板照抄 `/api/v1/ai/`；错误形状/限流器沿用 |
| 内容审核（五期） | 经 service 层 moderation 审查用户输入 |
| canvas-agent | Node 服务结构、skills store、MCP 接法、winston 日志照搬模式 |

## 里程碑概览与决策依赖

M0（1–2 天 spike）→ M1（最小链路）→ M2（工具与工作区）→ M3（模型矩阵与计费）。每期验收清单见总览文档；执行任务板见 plan 文档。

阻塞开工的决策：① 首发模型组合（决策项 2）；② `/office` 落位（决策项 3，默认现有 web）。阻塞实施的决策：边界值默认值表整体报批（决策项 8）。

## 开放问题

见总览文档「待用户决策项」8 条；spec 文档「未决问题登记」表逐条登记并标注阻塞对象。
