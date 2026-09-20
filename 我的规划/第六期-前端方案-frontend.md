# 第六期前端方案：/office 工作台 — Frontend Design

> 文档类型：Design（前端专项）。上游：`第六期-提案-proposal.md`（场景 S1–S4）、`第六期-接口规格-spec.md`（字段级契约）、`第六期-技术设计-design.md`（总体架构）。本文是前端实现的直接依据，与总览冲突时以本文为准（前端范围）。
> 状态：待评审。所有数值（尺寸/时长/阈值）为建议默认值，随边界值表一并报批。

## 0. 关键决策（先读这个）

| # | 决策 | 理由 |
|---|---|---|
| D1 | **SSE 客户端用 fetch + ReadableStream 手写解析，不用原生 EventSource** | 实测仓库请求层（`web/src/services/api/client.ts:67`）：鉴权是 `Authorization: Bearer <token>` 注入头 + 401 单飞刷新重放。EventSource **无法携带自定义请求头**，直接不可用；fetch 方案还能带 AbortSignal 支持取消 |
| D2 | 流式内容用「buffer + rAF 节流渲染」，不逐 delta setState | 长 run 每秒几十个 delta，逐个触发 markdown 重解析会卡；buffer 收集、每帧最多重渲染一次 |
| D3 | done 后**整体拉快照替换**该会话消息，不做逐条 diff 合并 | spec §7 快照权威的最简实现：流式阶段展示 buffer，终态后服务端快照是唯一事实；避免双份与合并 bug |
| D4 | office 用平台 antd 主题（app-theme.ts 全局 token），不引 canvasThemes | office 是产品工作台不是画布；遵守仓库「页面不自己写 dark?分支」规范 |
| D5 | 会话运行时状态进 Zustand（`stores/office.ts`），不做 props 层层透传 | 布局三层（列表/对话/抽屉）+ 输入区都要读写同一状态，遵守仓库 store 规范 |
| D6 | 消息/会话数据不落浏览器本地，权威在服务端 | 仓库规范：大对象不进本地存储；仅 draft、UI 偏好等极小项入 localforage |

## 1. 页面地图与路由集成

| 路由 | 组件 | 说明 |
|---|---|---|
| `/office` | `pages/office/index.tsx` | 工作台壳：无 `:sessionId` 时中区显示欢迎/空态，右抽屉收起 |
| `/office/s/:sessionId` | 同上 | 选中会话（URL 驱动，刷新/分享可恢复）；进入即拉快照并订阅流 |
| 全局导航 | `layouts/user-layout.tsx` 侧栏/顶部导航新增「AI 办公」入口 | 按 M3 权限点 `office.read` 控制可见（M1 仅内测角色可见） |

路由注册加进 `web/src/router.tsx`，复用现有登录守卫（未登录走既有跳转）。M1 前端入口由 feature flag（settings 功能开关）控制显隐，对应发布回滚点第①步。

## 2. 布局与视觉规格

```text
┌──────────────────────────────────────────────────────────────┐
│ OfficeShell（h-dvh, flex）                                    │
│ ┌──────────┐ ┌──────────────────────────────┐ ┌───────────┐ │
│ │ 会话栏    │ │ 对话区                        │ │ 抽屉       │ │
│ │ 264px    │ │ ┌──────────────────────────┐ │ │ 420px     │ │
│ │ 新建按钮  │ │ │ 重连 banner（条件渲染）     │ │ │ 可折叠    │ │
│ │ 搜索?M3  │ │ │ 消息流（overflow-y-auto）  │ │ │ artifact  │ │
│ │ 会话列表  │ │ │  …run 卡片…               │ │ │ 列表+详情  │ │
│ │ (虚拟列表 │ │ └──────────────────────────┘ │ │           │ │
│ │  M2 再上) │ │ 输入区（固定底部，自适应高度）    │ │           │ │
│ └──────────┘ └──────────────────────────────┘ └───────────┘ │
└──────────────────────────────────────────────────────────────┘
```

- 断点：≥1280 全三栏；1024–1280 左 232/右 360；<1024 左栏收起为图标条（点击浮出），抽屉改全屏覆盖层。
- 颜色/圆角/阴影：全部 antd token（`app-theme.ts`），组件内不出现硬编码色值；暗色模式跟随全局切换。
- 文案：中文，沿用仓库 i18n 约定。

## 3. 组件树（文件级）

```text
web/src/pages/office/
  index.tsx                    # 路由入口：解析 :sessionId，装配 OfficeShell
  use-office-chat.ts           # 核心 hook：SSE 生命周期状态机（§5）+ 渲染管线调度（§6）
  components/
    office-shell.tsx           # 三栏骨架 + 抽屉折叠状态
    session-list.tsx           # 会话列表：新建按钮、分页加载、当前项高亮
    session-item.tsx           # 单项：标题、最后活动时间、运行中状态点
    chat-panel.tsx             # 中区容器：banner + 消息流 + 输入区
    reconnect-banner.tsx       # 「连接中断，正在重连…」细条（streaming.connected=false 时）
    message-list.tsx           # 消息流容器（M1 普通滚动，M2 视量级再评估虚拟滚动）
    message-item.tsx           # user 气泡 / assistant 文档流（markdown 管线出口）
    run-card.tsx               # run 容器：状态条（queued/running+耗时/终态+用量点数）+ 内部内容
    tool-call-card.tsx         # 工具调用卡：折叠一行（图标+名+摘要+状态点）/展开 input·output 预览
    artifact-card.tsx          # 消息流内 artifact 引用卡（点击开抽屉定位）
    artifact-drawer.tsx        # 右抽屉：会话 artifacts 列表 + 详情
    artifact-viewer.tsx        # 详情：markdown 渲染 / monaco 只读（code/json）/ html 源码视图
    composer.tsx               # 输入区：textarea 自适应高度、附件按钮（M2）、发送/停止
    model-picker.tsx           # 模型选择（M3，按能力位过滤，M1 隐藏）
    error-card.tsx             # 终态错误卡：code→文案映射 + 重试按钮
web/src/stores/office.ts       # Zustand（§4）
web/src/services/api/office.ts # API 层（§7）
web/src/lib/office/sse.ts      # fetch-stream SSE 解析器（§5.1，纯函数可单测）
web/src/lib/office/markdown.ts # 渲染管线配置（sanitize 白名单、hljs 按需注册、katex）
```

组件私有样式用 Tailwind className；不新增全局 CSS。不建「只转发 props」的壳组件；跨层读写一律走 store。

## 4. 状态设计（stores/office.ts，Zustand）

```ts
type OfficeStream = {
  runId: string | null;
  lastSeq: number;              // 最近收到的事件 seq（重连用）
  connected: boolean;           // false → 显示重连 banner
  reconnectAttempt: number;
};

type OfficeState = {
  // 会话域
  sessions: SessionSummary[];
  sessionsLoading: boolean;
  currentSessionId: string | null;
  // 消息域（当前会话；快照权威）
  messages: MessageItem[];              // 快照加载后的权威列表
  materializedRunId: string | null;     // 已物化 run 的消息从快照来，流式 buffer 不再追加
  streamingText: string;                // D2：当前流式 buffer（rAF 消费后清空到展示态）
  displayText: string;                  // rAF 节流后的展示文本（触发渲染的唯一源）
  toolCalls: ToolCallItem[];            // 当前 run 的过程卡片（M2）
  // 运行域
  stream: OfficeStream;
  activeRun: { runId: string; status: "queued" | "running"; startedAt: number } | null;
  // 产物域
  artifacts: ArtifactItem[];
  activeArtifactId: string | null;
  drawerOpen: boolean;
  // 输入域
  draft: string;
  sending: boolean;
  // actions：loadSessions / createSession / openSession / send / cancelRun /
  //          applySnapshot / handleEvent（§5.2 事件表）/ appendDraft / stopStream
};
```

原则：组件只读状态 + 调 action；事件处理集中在 `handleEvent` 一处（§5.2 的表就是它的实现规格）；SSE 连接生命周期在 `use-office-chat.ts`，store 不持有连接对象。

## 5. SSE 客户端（lib/office/sse.ts + use-office-chat.ts）

### 5.1 fetch-stream 解析器（纯函数，可单测）

- `POST/GET + headers.Authorization`，`signal` 支持取消；`response.ok === false` 时按 status 分派（401 → 触发请求层单飞刷新后重试一次；410 → 抛 `EventsExpired`；409/429/400 → 抛 `ApiError`）。
- `ReadableStream + TextDecoder(stream:true)`，按行缓冲解析 `event:` / `data:` / `id:` 三字段；**跨 chunk 半包/粘包必须正确**（缓冲到换行为止；data 多行拼接为单事件 JSON）。这是单测重点。
- `id:` 字段即 seq → `store.stream.lastSeq`；重连时请求 `?lastSeq=<seq>`（等价 spec 的 Last-Event-ID 头，fetch 场景用显式参数）。

### 5.2 连接状态机与事件→状态变更表（handleEvent 规格）

连接：`openSession` → 拉快照 → 建 SSE → `connected=true`；`onerror`/流断 → `connected=false`，指数退避重连（1s/2s/4s/8s…上限 30s，**410 不重试** → 走快照重建，401 刷新后重试一次）；切换会话/组件卸载 → abort 当前连接。

| 事件 type | store 变更 |
|---|---|
| run_started | activeRun 置 running；materializedRunId 清空 |
| delta | streamingText += payload.text（进 buffer，rAF 搬运到 displayText） |
| tool_call / tool_result | toolCalls 按 toolCallId upsert（M2 渲染） |
| artifact | artifacts 列表插入 + 消息流尾部 artifact-card |
| usage | 不展示（M3 计费走服务端） |
| notice | 非阻断 toast/行内提示 |
| done | activeRun 清空 → 调 `GET messages` 整体替换 messages（D3）→ 清 streamingText/displayText → 刷新该会话 artifacts |
| error | activeRun 终态 → error-card（保留已生成的 streamingText 作为「已生成部分」展示）→ 同步拉快照对齐 |
| quota_exhausted | 提示弹层（M3），随后 error 收尾 |
| 未知 type | **静默忽略**（spec 向前兼容规则） |

重连恢复：`connected` 恢复后从 `lastSeq` 续流；若中途 run 已终态，服务端重放会补发 done/error，走同一事件表，无需特殊分支。

## 6. 渲染管线（lib/office/markdown.ts）

```text
displayText（增量字符串）
  → react-markdown（remark-gfm）
  → rehype-sanitize（白名单：文本/标题/列表/表格/代码块/引用/链接 http(s)·锚点；禁 script/iframe/style/事件属性）
  → 代码块：highlight.js（按需注册 ts/js/python/json/bash/sql/yaml/html/css/go 等常用集，控制 bundle）
  → 公式：rehype-katex（渲染失败降级显示原文 + console 记录）
  → message-item.tsx 输出
```

- **未闭合代码块**：流式期间检测 ``` 未配对，渲染时临时补闭合闭合线（仅展示层，不动 buffer）——否则半截代码块会把后续文本全吞进代码区。
- **节流**：rAF 每帧一次把 buffer 搬到 displayText（D2）；done 后快照替换，管线只跑最终文本一次。
- **html 类型 artifact**：viewer 只提供源码视图（等宽只读），不渲染富内容（评审 F7 修复项）；markdown artifact 渲染必经同一 sanitize 管线；monaco 只读模式加载 json/ts/js/python 等 `kind=code` 的 artifact。
- **XSS 断言**：单测用含 `<script>`、`onerror=`、`javascript:` 链接的样例文本，断言渲染产物无脚本节点（进 M2 验收）。

## 7. API 服务层（services/api/office.ts）

沿用共享 client（自动 /api/v1 前缀、Bearer、401 刷新重放、ApiError）。函数清单与 spec 端点一一对应：

```ts
createOfficeSession(agentId?)                  // A1
listOfficeSessions(cursor?)                    // A2
getOfficeSession(id)                           // A3
getOfficeMessages(id, afterTurnId?)            // A4（快照）
openOfficeStream(id, runId, lastSeq, signal)   // A6（返回 SSE 事件异步迭代器，走 sse.ts）
cancelOfficeRun(runId)                         // A7
listOfficeArtifacts(sessionId) / getOfficeArtifact(id) / officeArtifactDownloadUrl(id)  // A8–A10
requestFileUpload(...) / completeFileUpload(...)                                        // A11（M2）
listOfficeAgents()                             // A12（M3）
```

类型定义（`OfficeEvent` 联合类型、`SessionSummary`、`MessageItem`、`ArtifactItem`、`AgentSummary`）与 spec §2/§4 字段逐一对齐，不得本地改名。

## 8. 交互规格（逐状态）

| 状态 | 表现 |
|---|---|
| 发送 | Enter 发送 / Shift+Enter 换行；点击后 user 消息**乐观入列**（pending 态），201 回填 runId 与流式卡；409 → toast「上一条还在执行」并保留输入 |
| 运行中 | 发送钮变停止钮（cancel）；输入框可继续打字但不可再发（置灰 + 409 文案）；run 状态条显示耗时计时 |
| done | 状态条显示用量与点数（spec done payload）；快照替换后流式卡消失、正式消息呈现 |
| 失败 | error-card：code→中文文案映射（runtime_lost/stalled/upstream_timeout/orphan_reclaimed/credits_exhausted…）；`retryable=true` 显示「重试」按钮（同内容新 clientMsgId 重新发送） |
| 取消 | cancelled → 灰色终态条「已取消」+ 保留已生成部分 |
| 断线 | reconnect-banner；恢复自动补齐，无感 |
| 410 | 无感重建：拉快照重建列表，终态 run 显示最终状态 |
| 空态 | 无会话：中央欢迎区 + 四张场景快捷卡（S1 周报 / S2 表格 / S3 调研 / S4 代码，点击预填 composer prompt）；有会话无消息：仅欢迎文案 |
| 加载 | 会话列表骨架屏；快照加载 spinner；monaco/katex/hljs 懒加载（路由级 code-split） |

本地持久化（localforage，仅小项）：`draft`（按 sessionId）、`drawerOpen`、最后选中 sessionId。消息/会话/artifact 一律不落本地（D6）。

## 9. 实施切分（对应 plan 任务）

| plan 任务 | 前端子步骤 |
|---|---|
| T05 web 最小页 | a. 路由+Shell 布局+会话列表骨架 → b. sse.ts 解析器（先单测）+ store → c. 发送/流式渲染管线 → d. 终态/错误/断线/空态完备 |
| T08 工具卡片 | tool-call-card 折叠/展开 + toolCalls upsert |
| T10 artifacts | artifact-drawer/viewer（monaco 懒加载、净化断言、下载） |
| T13 模型选择 | model-picker 能力位过滤 |

## 10. 前端测试与验收点

1. sse.ts：半包/粘包/多行 data/跨 chunk 事件边界单测。
2. 净化：§6 XSS 断言（单测 + e2e）。
3. 重连：mock 断流 → 指数退避恢复 → lastSeq 续流补齐（fake office-agent）。
4. 410 → 快照重建路径；409 冲突提示；done → 快照替换无重复。
5. e2e（test-executor 编排，主会话浏览器执行）：S1 场景全流程 + 断网恢复 + 取消。

## 11. 对既有文档的修正登记

- **修正-1（spec §2.3 / design §2.5）**：SSE 客户端由「原生 EventSource」改为「fetch + ReadableStream + 手动 lastSeq」（D1，Bearer 鉴权不可用 EventSource）。`?lastSeq=` 参数语义与原 Last-Event-ID 头等价，服务端行为不变。
