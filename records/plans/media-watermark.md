---
plan_id: media-watermark
plan_version: 1
status: approved
objective: 免费用户（free/sunset）AI 生成的图片/视频在服务端烧录水印（展示与下载均为水印版）；付费用户（paid）不加水印且下发自动变干净（生成时留存干净原件）；干净原件仅可通过「申请下载」端点签发的 5 分钟签名 URL 获取（须同时通过 MediaAuth）；上传素材不打水印；审计走结构化日志不建表；本 feature 禁止一切 schema 变更。
recorded_at: 2026-09-18T00:00:00Z
updated_at: 2026-09-18T00:00:00Z
notes:
  branch: feature-admin-usage-analytics（用户明确指定在此分支开发；所有写操作前先 git branch --show-current 确认分支，切换过分支后每次写操作重新确认）
  baseline_commit: fab7d8e270ec130a22fb0d06c67f1efeb492b2c0
  protected_zone: admin/*、server/cmd/server/admin_routes.go、server/internal/db/version.go、server/internal/authz/modules.go、design/html/*（工作区用户未提交改动，全部只读，任何任务不得触碰）
  schema_freeze: 禁止一切 schema 变更，禁止 bump server/internal/db/version.go；不写旧数据兼容/迁移兜底（项目未上线）
  inputs:
    - technical-director 决策原文（波次划分、API 签名、异常矩阵、发布回滚、风险清单），2026-09-18 随本指派下发
    - 仓库证据核查（基线上确认存在）：server/internal/service/video_moderation.go:62 extractVideoFrames；server/internal/handler/ai.go failRequest 退款路径；server/internal/service/cleanup.go:50 Reclaim；server/internal/service/deletion.go:91 anonymizeOne（对象删除段约 135-170）；server/internal/storage/storage.go:31 Storage 接口、:40 Presign；server/internal/service/quota.go:69 QuotaService.DerivePlan；server/internal/config/config.go:18/124 JWTSecret；web/src/services/api/media.ts；web/src/pages/assets/index.tsx:154、web/src/pages/image/index.tsx:232、web/src/pages/video/index.tsx:255
---

# media-watermark：媒体服务端水印与干净原件安全下发

## 目标、背景与保护区

**目标**：免费用户（free/sunset）AI 生成的图片/视频由服务端烧录水印，展示与下载均为水印版；付费用户（paid）不加水印，且其（含由免费升级而来的）历史素材下发自动变干净——前提是生成时留存了干净原件（orig）；干净原件仅可通过「申请下载」端点 `POST /api/media/:storageKey/download` 签发的 5 分钟签名 URL 获取，且须同时通过 MediaAuth；上传素材不打水印（挂钩只在 AI 生成链路）；审计走结构化日志（如 `watermark_failed`），不建表。

**背景**：仓库 `/Users/lihanghang1/Desktop/TestProject/infinite-canvas`，当前分支 `feature-admin-usage-analytics`（用户明确指定在此分支开发），基线提交 `fab7d8e270ec130a22fb0d06c67f1efeb492b2c0`。

**保护区声明**：工作区存在用户另一批未提交改动——`admin/*`、`server/cmd/server/admin_routes.go`、`server/internal/db/version.go`、`server/internal/authz/modules.go`、`design/html/*`——全部划为保护区，任何任务不得触碰（只读）。因此本 feature **禁止一切 schema 变更**（不 bump db version、不新增/修改表列）。注意：`server/cmd/server/main.go` 与 `server/internal/config/config.go` 不在保护区，但属于共享落点，T4 与 T5 必须按「T4 验收后 T5 串行」避让 main.go 构造区。

**纪律**：所有写操作前先 `git branch --show-current` 确认在 `feature-admin-usage-analytics`；git 变更面以「基线 fab7d8e + 保护区未提交改动」为起点比对，只允许新增/修改任务表 write_scope 所列文件。

## 进展概览

进度：**1/8 已完成、0 进行中、0 阻塞**；计划已批准（2026-09-18，supervisor），波 1 T1/T2 已派发。

### 波次进度（当前状态总览）

| 波次 | 任务 | 并行性 | 状态 |
| --- | --- | --- | --- |
| 波 1 | T1 水印渲染包 / T2 存储层扩展 / T3 契约冻结 | 三任务全并行，零文件交集（go.mod/go.sum 由 T1 独占） | 全部未开始 |
| 波 2 | T4 下发/下载闸门 | 依赖波 1 验收 | 未开始 |
| 波 2 | T5 生成落盘挂钩 | T4 验收后串行（避让 main.go 构造区） | 未开始 |
| 波 2 | T6 orig 连带清理 | 依赖 T2，与 T4/T5 并行 | 未开始 |
| 波 2 | T7 前端下载流程 | 依赖 T3，与 T4 并行 | 未开始 |
| 持续 | T8 测试设计（test-planner） | 依赖 T3，用例库先行，端到端用例须在评审门前通过 | 未开始 |

### 三读总览

| 事项 | 业务侧解读 | 技术侧解读 | 交付效果（可验证） |
| --- | --- | --- | --- |
| 水印渲染包（T1） | 免费用户生成的图片/视频自带「infinite-canvas」水印 | server/internal/watermark 包：图片重编码烧录、视频 ffmpeg 平铺 overlay | 图片三种格式编码/失败矩阵单测、TilePNG 像素断言（水印后字节必变） |
| 存储层扩展（T2） | 每份生成物可另存一份干净原件 | Storage 接口新增 PresignWithTTL；orig 路径 + S3 负缓存 + HasOriginal | OrigPath/负缓存/零值语义单测通过 |
| 契约冻结（T3） | 「申请下载」先签限时链接，再凭链接取件 | POST /download 与 GET /api/media-download/:token 的请求/响应/token 编码定稿 | 契约写入本计划「接口契约」节，T7 按此实现 |
| 下发/下载闸门（T4） | 付费用户看/下载干净版，免费用户始终看水印版 | 按媒体行归属者 DerivePlan(owner) 选字节 + 缓存头分级 + 签名 token 校验链 | 三个泄漏面单测锁死（社区浏览者/篡改 token/S3 TTL） |
| 生成落盘挂钩（T5） | 水印失败宁可退款失败，绝不漏出干净件 | ai.go / aitask.go fail-closed：水印→orig→Save 的落盘序与补偿 | 注入水印错误断言无任何干净字节落盘/可下发 |
| 连带清理（T6） | 删除素材、注销账号后原件同步消失 | Reclaim 与注销匿名化补删 orig（best-effort + error 日志） | 删除/注销后 orig 对象同步消失的单测 |
| 前端下载（T7） | 下载按钮改为「先申请、再取件」 | requestDownload → fetch(带凭据) → blob → saveAs，三页接入 | npm run typecheck && npm test 通过，画布导出链路不改 |
| 测试设计（T8） | 用例库先归档，验收有据可查 | records/tests/infinite-canvas-media-watermark/ 用例库 + smoke 集 | smoke 集在评审门关闭前全部通过 |

**我们在哪**：计划定稿待批准，尚无代码改动；所有代码落点已在基线核实存在。
**下一步**：supervisor 批准本计划（含边界值默认值确认）→ 并行派发波 1（T1/T2/T3）。
**阻塞风险**：保护区禁改与 schema 冻结（硬约束）；ffmpeg/ffprobe 与 CJK 字体文件是测试与部署环境前提；`WATERMARK_ENABLED` 等边界值默认值需随批准一并向用户确认（AGENTS.md 边界值纪律）。

## 任务清单

状态枚举（执行时仅写回「状态」与「证据（evidence）」两列，最小 diff，不改已完成任务行）：`pending`（未开始）/ `in_progress`（进行中）/ `done`（已完成）/ `blocked`（阻塞）。

| ID | 任务 | 优先级 | write_scope（代码落点） | 依赖 | 验收命令 | 状态 | 证据（evidence） |
| --- | --- | --- | --- | --- | --- | --- | --- |
| T1 | 水印渲染包 | P0 | 新增 `server/internal/watermark/`（service.go、image.go、video.go、tile.go 及同名 _test.go）；`server/go.mod`、`server/go.sum`（T1 独占，波 1 零交集的前提） | — | `cd server && go build ./... && go test ./...` | pending | （执行时写回：命令+结果摘要） |
| T2 | 存储层扩展（PresignWithTTL / orig / 负缓存） | P0 | `server/internal/storage/storage.go`、`local.go`、`s3.go`；新增 `server/internal/storage/orig.go` 及 orig 测试 | — | `cd server && go build ./... && go test ./...` | pending | （执行时写回） |
| T3 | 契约冻结（无代码） | P0 | 无代码；契约全文落在本计划「接口契约」节 | — | —（评审确认契约文本） | done | 契约全文见本计划「接口契约」节（POST /download 两态响应、GET token 校验链、token 编码、包 API、存储接口变更），supervisor 批准确认 |
| T4 | 下发/下载闸门 | P0 | `server/internal/handler/media.go`；新增 `server/internal/handler/media_download.go`；`server/cmd/server/main.go`（路由注册与 Service 注入）；`server/internal/config/config.go`；对应 _test.go | T1、T2、T3 | `cd server && go build ./... && go test ./...` | pending | （执行时写回） |
| T5 | 生成落盘挂钩（fail-closed） | P0 | `server/internal/handler/ai.go`；`server/internal/service/aitask.go`；对应 _test.go | T4（验收后串行，避让 main.go 构造区） | `cd server && go build ./... && go test ./...` | pending | （执行时写回） |
| T6 | orig 连带清理 | P1 | `server/internal/service/cleanup.go`、`server/internal/service/deletion.go`；对应 _test.go | T2（与 T4/T5 并行） | `cd server && go build ./... && go test ./...` | pending | （执行时写回） |
| T7 | 前端下载流程 | P1 | `web/src/services/api/media.ts`；`web/src/pages/assets/index.tsx`、`web/src/pages/image/index.tsx`、`web/src/pages/video/index.tsx` | T3（与 T4 并行） | `cd web && npm run typecheck && npm test` | pending | （执行时写回） |
| T8 | 测试设计（test-planner，角色契约新增） | P1 | `records/tests/infinite-canvas-media-watermark/`（用例库；契约参照 `records/tests/README.md`，若尚不存在由 supervisor 先建立） | T3 | 用例库 smoke 集在评审门关闭前全部通过 | pending | （执行时写回） |

### 任务详情

**T1 水印渲染包**：新增 `server/internal/watermark/`。API：`type Service struct`；`NewService(fontPath, text string)`；`(s) Image(src []byte, srcMime string) (out []byte, outMime string, err)`；`(s) Video(ctx, src []byte, srcMime string) ([]byte, error)`；`(s) TilePNG(width, height int) ([]byte, error)`；哨兵错误 `ErrUnsupportedFormat`。图片支持 jpeg/png（原格式重编码，jpeg 质量 90）、webp（x/image/webp 解码 → PNG 输出，mime 变更为 image/png）；gif 及其它返回 `ErrUnsupportedFormat`。视频仅 video/mp4：Go 渲染整帧平铺水印 PNG（复用 TilePNG；字号 `max(16, 帧宽/28)`、平铺间距 4×字号、倾斜 -30°、白色文字 1px 深色描边、透明度 0.32）+ ffprobe 读帧尺寸 + ffmpeg overlay（`-filter_complex "[1][0]scale2ref[wm][base];[base][wm]overlay=0:0" -c:v libx264 -preset veryfast -crf 23 -pix_fmt yuv420p -movflags +faststart -c:a copy`），超时 `WATERMARK_TIMEOUT` 默认 120s；沿用 `server/internal/service/video_moderation.go:62` extractVideoFrames 的 LookPath/MkdirTemp/exec.CommandContext 范式。水印文案 const 默认 `infinite-canvas`，`WATERMARK_TEXT` 覆盖（≤40 字符），`WATERMARK_FONT_PATH` 指定 CJK 字体。go.mod 新增 `golang.org/x/image`（T1 独占 go.mod/go.sum）。单测：图片三种格式编码/失败矩阵、TilePNG 尺寸与像素断言（水印后字节必变）、ffmpeg 缺失时跳过或明确失败（不允许静默假绿）。

**T2 存储层扩展**：`server/internal/storage/storage.go` 接口新增 `PresignWithTTL(ctx, path string, ttl time.Duration) (Presigned, error)`，现有 `Presign`（storage.go:40）退化为委托调用（老调用点不动）；`local.go`、`s3.go` 实现 PresignWithTTL；新增 `orig.go`：`OrigPath(userID, storageKey string) string = "{uid}/orig/{冒号后id}"`（无扩展名）+ S3 负缓存（进程内 map+RWMutex，仅缓存「无 orig」负结论；local 驱动可不做）+ `HasOriginal(ctx, stor, uid, key)` 便捷判定。单测：OrigPath 拼装与路径穿越用例、负缓存命中/穿透、local PresignWithTTL 零值语义。

**T3 契约冻结**：无代码。把 POST /api/media/:storageKey/download 响应 `{ "url": string, "expiresAt": string|null }`（paid 有 orig → url=/api/media-download/{token} + expiresAt RFC3339；paid 无 orig 或 free/sunset → url=/api/media/{key} + expiresAt=null）与 token 编码写进本计划「接口契约」节即完成。

**T4 下发/下载闸门**（先于 T5，两者都要动 main.go 构造区）：
- `server/internal/handler/media.go`：Get/Head/serveLocal/redirectToPresigned 按归属者档位选字节——`DerivePlan(owner)`（`server/internal/service/quota.go:69`，注意是媒体行 user_id 而非浏览者）；plan!=paid → 直接服务现有对象；plan==paid → 负缓存/Stat orig，命中服务 orig，未命中服务现对象并记负缓存。缓存头分级：可变字节 `Cache-Control: private, no-cache` + ETag `wm-{checksum}`/`orig-{checksum}` + serveLocal 补 If-None-Match→304；稳定字节维持现状 immutable；S3 分支只换 Location 为 orig 的 PresignWithTTL(360s)，302 缓存头维持现状；Head 与 Get 同口径。Delete 补删 orig，best-effort。
- 新增 `server/internal/handler/media_download.go`：POST /download 用**严格归属查询 user_id=当前用户 AND storage_key，禁走 findOwned/社区放行分支**，归属不符 404；HMAC 密钥 = HMAC-SHA256(key=JWTSecret, msg="ic-media-download-v1") 摘要；token = b64url(payload)+"."+b64url(sig)，payload = ver(1B)=1 ‖ exp(8B unix BE) ‖ uid(16B raw uuid) ‖ keyLen(2B BE) ‖ storageKey ‖ nonce(8B)；TTL 钳制 ≤300s；paid 无 orig 直接返回 media url。GET /api/media-download/:token：校验链 hmac.Equal → ver=1 → exp → token uid == 当前用户 → media_files 归属行仍存在 → 流式回流 orig（Content-Type/文件名嗅探、Content-Disposition: attachment、Cache-Control: private, no-store、nosniff），S3 302 PresignWithTTL(360s)；任何校验失败一律 404 防探测。
- `server/cmd/server/main.go`：POST 挂现有 media 组；GET 新组 /api/media-download 挂同款 MediaAuth+active+passwordGate；watermark.Service 构造注入 media handler/ai handler/aiTaskService——注入点以实际代码为准。
- `server/internal/config/config.go`：新增 `WATERMARK_ENABLED`（默认 false）、`WATERMARK_FONT_PATH`、`WATERMARK_TEXT`；`WATERMARK_ENABLED=true` 且字体缺失/不可读 → 启动报错 fail-fast。修正（批准时裁定）：`WATERMARK_TIMEOUT` 不进 config.go，由 watermark 包按仓库先例（`video_moderation.go:19 videoSampleFPS` 直接读 env）读取，默认 120s，供 `Video()` 内部 `context.WithTimeout` 使用。
- POST /download 挂限流（复用现有 limiter 模式，60 次/小时/用户）。
- 单测必须含三个泄漏面锁死用例：①社区浏览者 POST 已发布作品的 /download → 404；②token 过期/篡改/跨用户 → 404；③S3 预签名 TTL ≤360s。

**T5 生成落盘挂钩**（T4 验收后串行）：`server/internal/handler/ai.go`（图片：审核通过后、Save 前插入——owner DerivePlan 为 free/sunset 时：watermark.Image 失败 → failRequest（既有退款路径，ai.go:1440-1453 一带）；成功 → storage.Put(orig) 失败 → 整单失败；Save 成功后若 Save 报错补偿 Delete(orig)。paid → 现状不变，不存 orig）；`server/internal/service/aitask.go`（succeedTask 同构：水印失败 → s.failTask 既有退款；写盘序同上）。上传素材链路不挂钩。单测锁死 fail-closed：伪造 free 档 + 注入水印错误 → 断言无任何干净字节落盘/可下发路径。

**T6 orig 连带清理**：`server/internal/service/cleanup.go:129` 一带 Reclaim、`server/internal/service/deletion.go:135-170` 注销匿名化，补 `storage.Delete(origPath)` best-effort + error 日志。单测：删除媒体/注销后 orig 对象同步消失。

**T7 前端下载流程**：`web/src/services/api/media.ts` 新增 `requestDownload(storageKey)` → POST /api/media/{key}/download 返回 `{url, expiresAt}`；三个调用点改为「先 requestDownload → fetch(url, 带凭据) → blob → saveAs」：`web/src/pages/assets/index.tsx:154` downloadAsset、`web/src/pages/image/index.tsx:232` downloadImage、`web/src/pages/video/index.tsx:255` downloadVideo。canvas/project.tsx 与画布导出链路确认不改（下发层已按档位出字节）。检查：`npm run typecheck && npm test`。

**T8 测试设计**（本计划按角色契约新增，TD 清单外）：test-planner 产出用例库 `records/tests/infinite-canvas-media-watermark/`，覆盖：T3 契约用例（响应两态）、三个泄漏面、缓存头分级（可变/稳定字节）、fail-closed 落盘序、格式矩阵（jpeg/png/webp/gif/mp4/字体缺失）、T6 清理、T7 三调用点；产出 smoke 集，「验证命令」节引用之；端到端用例须在评审门关闭前通过。

### 未决项

| 项 | 说明 | 处理建议 |
| --- | --- | --- |
| T8 为新增任务 | TD 任务清单为 T1-T7；用户可见功能按角色契约必须有 test-design 任务，故新增 T8 | supervisor 批准时确认；若显式裁撤，需接受评审门缺少 smoke 集引用的后果 |
| 边界值默认值待批 | WATERMARK_TIMEOUT=120s、token TTL≤300s、S3 预签名 360s、限流 60 次/小时/用户、文案 ≤40 字符、jpeg q90、平铺参数（字号 max(16,帧宽/28)/间距 4×/-30°/0.32）、WATERMARK_ENABLED 默认 false | 随本计划批准一并向用户确认（AGENTS.md：边界值不得静默加入）；执行中不得调整 |
| ffmpeg/ffprobe 与 CJK 字体 | T1/T4 的测试与运行环境前提；flag=true 且字体缺失时按设计启动报错 | 部署前置清单：PATH 内 ffmpeg/ffprobe + WATERMARK_FONT_PATH 指向可用字体文件 |
| orig 孤儿长期策略 | T6 双入口补删 best-effort 之外的残余孤儿无自动回收 | 本期不做；后续以存储生命周期策略或定期扫描待办承接 |

## 接口与共享机制

| 机制 | 注册点 | 消费点 | 说明 |
| --- | --- | --- | --- |
| watermark.Service（NewService/Image/Video/TilePNG/ErrUnsupportedFormat） | T1 `server/internal/watermark/` | T5 生成挂钩（ai.go / aitask.go）、T4 构造注入 | 图片 jpeg/png 原格式重编码（jpeg q90）、webp→PNG（mime 变 image/png）、gif/其它 ErrUnsupportedFormat；视频仅 mp4 |
| Storage.PresignWithTTL(ctx, path, ttl) (Presigned, error) | T2 storage.go 接口 | T4 media.go S3 分支、media_download.go | 现有 Presign 退化为委托调用，老调用点不动 |
| OrigPath / HasOriginal / S3 负缓存 | T2 orig.go | T4 选字节、T5 落盘、T6 清理 | 负缓存仅缓存「无 orig」结论，进程内 map+RWMutex；local 驱动可不做 |
| QuotaService.DerivePlan（既有） | `server/internal/service/quota.go:69` | T4 下发选字节、T5 生成挂钩 | 一律取媒体行 user_id（归属者），绝不取浏览者 |
| POST /download + GET /api/media-download/:token + HMAC token | T4 media_download.go；main.go 注册 | T7 requestDownload | 契约全文见「接口契约」节 |
| WATERMARK_ENABLED / WATERMARK_FONT_PATH / WATERMARK_TEXT / WATERMARK_TIMEOUT | T4 config.go | T1 构造参数、T5 开关 | ENABLED 默认 false；启用时字体缺失 fail-fast |
| MediaAuth + active + passwordGate（既有中间件） | 既有 | T4 新路由组 | /api/media-download 与现有 media 组同款 |
| 限流 limiter（既有模式） | 既有 | T4 POST /download | 60 次/小时/用户 |
| 结构化日志（watermark_failed 等） | T4/T5 | 审计与灰度观察 | 不建表（schema 冻结） |

## 接口契约（T3 冻结）

### POST /api/media/:storageKey/download（挂现有 media 组，MediaAuth，限流 60 次/小时/用户）

- 归属判定：**严格 `user_id = 当前用户 AND storage_key = :storageKey`，禁走 findOwned/社区放行分支**；归属不符一律 404（防探测）。
- 成功响应 200：`{ "url": string, "expiresAt": string|null }`
  - paid 且存在 orig → `url = /api/media-download/{token}`，`expiresAt` = RFC3339（now+TTL，TTL 钳制 ≤300s）
  - paid 无 orig，或 free/sunset → `url = /api/media/{key}`（现有下发 URL），`expiresAt = null`

### GET /api/media-download/:token（新组，MediaAuth + active + passwordGate）

- 校验链（顺序）：`hmac.Equal` → ver=1 → exp 未过期 → token uid == 当前用户 → media_files 归属行仍存在；**任何一步失败一律 404**。
- 成功：流式回流 orig——Content-Type/文件名嗅探、`Content-Disposition: attachment`、`Cache-Control: private, no-store`、`X-Content-Type-Options: nosniff`；S3 驱动 → 302 到 orig 的 PresignWithTTL(360s)。

### token 编码

- HMAC 密钥：`HMAC-SHA256(key = JWTSecret, msg = "ic-media-download-v1")` 的摘要作为密钥（只派生，不落盘、不输出任何密钥值）。
- `token = b64url(payload) + "." + b64url(sig)`，`sig = HMAC-SHA256(key = 上述派生密钥, msg = payload)`。
- payload 字节布局：`ver(1B)=1 ‖ exp(8B unix BE) ‖ uid(16B raw uuid) ‖ keyLen(2B BE) ‖ storageKey(UTF-8) ‖ nonce(8B)`。

### watermark 包 API 签名（T1）

```go
type Service struct{ /* fontPath, text */ }
func NewService(fontPath, text string) *Service
func (s *Service) Image(src []byte, srcMime string) (out []byte, outMime string, err error)
func (s *Service) Video(ctx context.Context, src []byte, srcMime string) ([]byte, error)
func (s *Service) TilePNG(width, height int) ([]byte, error)
var ErrUnsupportedFormat = errors.New(...)
```

### 存储接口变更（T2）

- `Storage` 接口（storage.go:31）新增：`PresignWithTTL(ctx context.Context, path string, ttl time.Duration) (Presigned, error)`；现有 `Presign(ctx, path)` 保留并退化为委托调用（语义等价既有行为）。
- 新增 `orig.go`：`OrigPath(userID, storageKey string) string`（`"{uid}/orig/{冒号后id}"`，无扩展名）、`HasOriginal(ctx, stor, uid, key)`；S3 负缓存仅缓存「无 orig」负结论。

### 边界值与默认值（随本计划批准一并向用户确认）

| 项 | 默认值 | 适用环节 | 失败后处理 |
| --- | --- | --- | --- |
| WATERMARK_ENABLED | false | 生成链路是否烧录水印 | flag=false 即全量回滚点 |
| WATERMARK_TIMEOUT | 120s | 视频转码 | 超时→fail-closed 退款 |
| token TTL | ≤300s（钳制） | 申请下载签发 | 过期→404 |
| S3 预签名 TTL | 360s | 下发/取件 302 | 到期后链接失效，重新申请 |
| 限流 | 60 次/小时/用户 | POST /download | 超限拒绝，不影响展示 |
| WATERMARK_TEXT | `infinite-canvas`，覆盖 ≤40 字符 | 水印文案 | 超长截断或启动校验拒绝（以实现为准，不得静默） |
| 图片质量/平铺参数 | jpeg q90；字号 max(16,帧宽/28)、间距 4×字号、-30°、alpha 0.32 | 烧录 | 仅影响观感，不改变失败语义 |

## 异常矩阵

| 关键路径 | 异常 | 检测 | 处理 | 重试责任方/次数/退避 | 幂等前提 | 用户可见结果 |
| --- | --- | --- | --- | --- | --- | --- |
| A 生成落盘（ai.go / aitask.go） | 视频水印超时 | exec.CommandContext 按 WATERMARK_TIMEOUT=120s 截断 | fail-closed：failRequest / failTask 既有退款路径，不落盘 | N/A——不自动重试，用户可重新发起生成 | N/A——每次生成独立 storageKey | 生成失败并退款提示 |
| A 生成落盘 | 重复请求/任务回调双写 | Save 与 succeedTask 并发写同 key | storage.Put 按 storageKey 幂等覆盖 orig；Save 报错补偿 Delete(orig) | 补偿 Delete best-effort 一次，失败仅日志（T6 兜底） | storageKey 唯一 | 无感知 |
| A 生成落盘 | 状态冲突：orig 已存在/负缓存残留 | Put 覆盖写；下发时 Stat/HasOriginal 校正并清负缓存 | 覆盖写 + 负缓存自愈 | N/A——无重试语义 | 同上 | 短暂旧水印版，随后自动变干净 |
| A 生成落盘 | 回调失败（水印失败/orig Put 失败/Save 失败） | 错误返回值 | fail-closed：水印失败→退款；Put orig 失败→整单失败退款；Save 失败→补偿删 orig | 无自动重试（N/A），失败即退款 | 同上 | 失败 + 退款；绝不出现干净字节可下发 |
| B 下发（media.go Get/Head） | 超时 | N/A——本地流式，无外呼；S3 302 后由客户端直连，360s 签名有效期兜底 | N/A | N/A | N/A | N/A |
| B 下发 | 重复请求 | N/A——按档位即时选字节，无状态变更 | 天然幂等 | N/A | N/A | 结果一致 |
| B 下发 | 状态冲突：paid 无 orig（负缓存/Stat 未命中） | Stat / HasOriginal 未命中 | 回退服务现对象并记负缓存（fail-closed 方向：宁可水印版，不泄漏） | N/A | N/A | 仍拿到水印版，不泄漏干净件 |
| B 下发 | 回调失败：S3 PresignWithTTL 失败 | 错误返回 | 返回 500，不回退到无签名长 URL | N/A | N/A | 下载/取件失败，可重试 |
| C 申请下载（POST /download） | 重复请求 | N/A——签发无状态 | 每次签发新 token，旧 token 有效至 exp；限流 60 次/小时/用户 防滥用 | N/A | nonce(8B) 保证 token 唯一 | 均拿到有效链接 |
| C 申请下载 | token 过期/篡改/跨用户/归属行已删 | 校验链逐项失败 | 一律 404 防探测 | N/A | N/A | 404 |
| C 申请下载 | 超时 | exp 字段；TTL ≤300s | 过期 404 | N/A | N/A | 链接过期需重新申请 |
| D 清理（cleanup.go / deletion.go） | 重复请求/对象已不存在 | Delete 返回不存在 | Delete 幂等，视为成功 | N/A | 对象键幂等 | 无感知 |
| D 清理 | 回调失败：Delete orig 失败 | error 返回 | best-effort + error 日志，不阻塞主流程 | N/A——不自动重试 | 同上 | 无感知（残余孤儿见未决项） |

## 发布顺序与回滚点

| 阶段 | 内容 | 回滚点 |
| --- | --- | --- |
| ① 合并上线 | `WATERMARK_ENABLED=false` 合并：生成链路不烧录；下发闸门与 token 校验在线但因无 orig 自然回退现对象，行为等同现状 | 关闭 `WATERMARK_ENABLED`（或 revert 合并）；flag 关闭即等效回滚 |
| ② 测试环境验证 | 开 `true` + 配置 `WATERMARK_FONT_PATH`：验证新产物水印 + orig 双写 + 申请下载全链路 | 关闭 flag |
| ③ 真库确认 | 真实 MySQL 上跑一次迁移 + 启动确认（无 schema 变更仍执行，防 MySQL 特有回归——AGENTS.md 纪律） | 关闭 flag |
| ④ 灰度观察 | 观察 `watermark_failed` 等结构化日志一周 | 关闭 flag |

**执行注意（flag 语义）**：下发闸门与 token 校验**不得**随 `WATERMARK_ENABLED` 关闭而下线——已存 orig 的付费用户若回退到水印主对象即属回归；flag 只控制生成时是否烧录。回滚后新增免费生成不再打水印、历史水印对象维持现状，属可接受状态。

## 风险

| 级别 | 风险 | 缓解 / 归属 |
| --- | --- | --- |
| S1 | 社区放行：POST /download 若走 findOwned/社区放行分支，社区浏览者可取得干净件 | T4 严格归属查询 + 泄漏面单测①（404） |
| S1 | S3 预签名 TTL 过长造成干净件长期可直链访问 | T4 PresignWithTTL(360s) + 泄漏面单测③ |
| S1 | 水印失败回退成干净下发 | T5 fail-closed：失败即退款，无干净字节落盘；单测锁死注入错误场景 |
| S2 | 缓存串档：可变字节被浏览器/CDN 缓存导致档位错乱 | T4 缓存头分级（private, no-cache + ETag wm-/orig- + 304；稳定字节维持 immutable；token 流 no-store） |
| S2 | 格式/字体失败率：webp/gif/字体缺失推高失败率 | T1 格式矩阵单测 + 启动 fail-fast + watermark_failed 日志灰度观察 |
| S2 | orig 孤儿：媒体行已删但 orig 残留 | T6 双入口补删 + error 日志；长期策略见未决项 |
| S3 | 历史干净件：上线前 free 用户已生成的素材仍是干净版，本期不追溯回填 | 已知限制，接受；后续如需回填另立待办 |
| S3 | 视频收敛延迟：ffmpeg 转码耗时/任务积压 | WATERMARK_TIMEOUT=120s + preset veryfast + 灰度观察 watermark_failed |
| S4 | HEAD 不一致与 EXIF：Head/Get 口径不一；EXIF 元数据随原图泄漏 | T4 Head 与 Get 同口径单测；EXIF 由重编码（jpeg q90 / PNG 重编码 / webp→PNG）剥离，T1 测试覆盖 |

## 验证命令

| 范围 | 命令 / 步骤 | 预期 |
| --- | --- | --- |
| T1/T2/T4/T5/T6（server） | `cd server && go build ./... && go test ./...` | 全绿；T4 含三个泄漏面用例，T5 含 fail-closed 用例，结果摘要写回任务表 evidence 列 |
| T7（web） | `cd web && npm run typecheck && npm test` | 全绿；画布导出链路无改动 |
| T8 smoke 集 | 运行 `records/tests/infinite-canvas-media-watermark/` 用例库的 smoke 子集 | 评审门关闭前全部通过，含端到端：flag=true 下免费账号生成→展示/下载均水印；付费（或升级）账号→POST /download 签发→GET token 流式取干净件；过期/篡改/跨用户 token→404 |
| 集成 | supervisor 全量检查 + git 变更面比对 write_scope | 起点=基线 fab7d8e + 保护区未提交改动；仅允许新增/修改任务表所列文件，保护区零触碰 |
| 真库验证 | 真实 MySQL 跑一次迁移 + 启动 | 迁移与启动成功（无 schema 变更仍执行） |
| 环境前提（测试替身） | ffmpeg/ffprobe 在 PATH；CJK 字体 fixture；样本夹具 jpeg/png/webp/gif/mp4 各一 | 缺失时视频用例明确 skip 或 fail，不允许静默假绿 |

**评审门（material 变更，独立 reviewer，作者不自审）**：重点核对三个泄漏面单测、缓存头分级、fail-closed、保护区零触碰；端到端用例先于评审门通过。
