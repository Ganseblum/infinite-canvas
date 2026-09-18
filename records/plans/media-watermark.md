---
plan_id: media-watermark
plan_version: 2
status: approved
objective: 免费用户（free/sunset）AI 生成的图片/视频在服务端烧录水印（展示与下载均为水印版）；付费用户（paid）不加水印且下发自动变干净（生成时留存干净原件）；干净原件仅可通过「申请下载」端点签发的 5 分钟签名 URL 获取（须同时通过 MediaAuth）；上传素材不打水印；审计走结构化日志不建表；本 feature 禁止一切 schema 变更。
recorded_at: 2026-09-18T00:00:00Z
updated_at: 2026-09-18T00:00:00Z
notes:
  branch: feature-admin-usage-analytics（用户明确指定在此分支开发；所有写操作前先 git branch --show-current 确认分支）
  baseline_commit: fab7d8e270ec130a22fb0d06c67f1efeb492b2c0 → 执行期间用户并行提交 admin/main-site 五笔，波 2 起变更面基线以 19f5cd4270ec130a22fb0d06c67f1efeb492b2c0 之后的工作区为准（原保护区文件已随之入库，仍禁改）
  incident: v1 计划文件曾被并行会话清理 untracked 文件时连带删除（design/html 草稿同批丢失，无法恢复）；本文件为 v2 重建版，内容与 v1+批准裁定一致，并补写波 1 验收证据
  protected_zone: admin/*、server/cmd/server/admin_routes.go、server/internal/db/version.go、server/internal/authz/modules.go、design/html/*（只读，任何任务不得触碰）
  schema_freeze: 禁止一切 schema 变更，禁止 bump server/internal/db/version.go；不写旧数据兼容/迁移兜底（项目未上线）
  inputs:
    - technical-director 决策原文（波次划分、API 签名、异常矩阵、发布回滚、风险清单），2026-09-18
    - 仓库证据核查：server/internal/service/video_moderation.go:62 extractVideoFrames；server/internal/handler/ai.go failRequest 退款路径；server/internal/service/cleanup.go:50 Reclaim；server/internal/service/deletion.go:91 anonymizeOne；server/internal/storage/storage.go:31 Storage 接口、:40 Presign；server/internal/service/quota.go:69 QuotaService.DerivePlan；server/internal/config/config.go:18/124 JWTSecret；web/src/services/api/media.ts；web/src/pages/assets/index.tsx:154、web/src/pages/image/index.tsx:232、web/src/pages/video/index.tsx:255
---

# media-watermark：媒体服务端水印与干净原件安全下发

## 目标、背景与保护区

**目标**：免费用户（free/sunset）AI 生成的图片/视频由服务端烧录水印，展示与下载均为水印版；付费用户（paid）不加水印，且其（含由免费升级而来的）历史素材下发自动变干净——前提是生成时留存了干净原件（orig）；干净原件仅可通过「申请下载」端点 `POST /api/media/:storageKey/download` 签发的 5 分钟签名 URL 获取，且须同时通过 MediaAuth；上传素材不打水印（挂钩只在 AI 生成链路）；审计走结构化日志（如 `watermark_failed`），不建表。

**背景**：仓库 `/Users/lihanghang1/Desktop/TestProject/infinite-canvas`，当前分支 `feature-admin-usage-analytics`（用户明确指定在此分支开发），基线提交 `fab7d8e270ec130a22fb0d06c67f1efeb492b2c0`（执行中前移至 `19f5cd4`，见 notes）。

**保护区声明**：`admin/*`、`server/cmd/server/admin_routes.go`、`server/internal/db/version.go`、`server/internal/authz/modules.go`、`design/html/*` 全部划为保护区，任何任务不得触碰。本 feature **禁止一切 schema 变更**（不 bump db version、不新增/修改表列）。`server/cmd/server/main.go` 与 `server/internal/config/config.go` 属共享落点，T4 与 T5 必须按「T4 验收后 T5 串行」避让 main.go 构造区。

**纪律**：所有写操作前先 `git branch --show-current` 确认在 `feature-admin-usage-analytics`；git 变更面以「基线 + 保护区」为起点比对，只允许新增/修改任务表 write_scope 所列文件。

## 进展概览

进度：**8/8 已完成、0 进行中、0 阻塞**；T1-T8 全部验收（supervisor 编译复核：`go test -count=1 ./...` exit 0 / 14 包 ok，web typecheck+test 绿，变更面 31 项全部在 write_scope 内、保护区零触碰）。下一步：独立 reviewer 评审门 → 文档收尾 → 用户测试。

### 波次进度（当前状态总览）

| 波次 | 任务 | 并行性 | 状态 |
| --- | --- | --- | --- |
| 波 1 | T1 水印渲染包 / T2 存储层扩展 / T3 契约冻结 | 三任务全并行，零文件交集 | ✅ 全部完成 |
| 波 2 | T4 下发/下载闸门 | 依赖波 1 验收 | 进行中 |
| 波 2 | T5 生成落盘挂钩 | T4 验收后串行（避让 main.go 构造区） | 未开始 |
| 波 2 | T6 orig 连带清理 | 依赖 T2，与 T4/T5 并行 | 进行中 |
| 波 2 | T7 前端下载流程 | 依赖 T3，与 T4 并行 | 进行中 |
| 持续 | T8 测试设计（test-planner） | 依赖 T3，用例库先行 | 进行中 |

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

**我们在哪**：波 1 验收通过，波 2 执行中。
**下一步**：T4 验收 → 串行派发 T5 → 全量集成检查 → 独立 reviewer 评审门。
**阻塞风险**：无当前阻塞；ffmpeg/ffprobe 与 CJK 字体文件是部署前提（灰度清单见「未决项」）。

## 任务清单

状态枚举（执行时仅写回「状态」与「证据（evidence）」两列，最小 diff）：`pending` / `in_progress` / `done` / `blocked`。

| ID | 任务 | 优先级 | write_scope（代码落点） | 依赖 | 验收命令 | 状态 | 证据（evidence） |
| --- | --- | --- | --- | --- | --- | --- | --- |
| T1 | 水印渲染包 | P0 | 新增 `server/internal/watermark/`（service.go、image.go、video.go、tile.go 及同名 _test.go）；`server/go.mod`、`server/go.sum`（T1 独占） | — | `cd server && go build ./... && go test ./...` | done | 2026-09-18 全绿：build 通过，全仓 `go test ./...` ok；watermark 包非缓存 `-count=1` ok，14 用例 0 skip 0 fail（本机 ffmpeg 8.1 在，TestVideoTranscode 真实转码 + ffprobe 校验输出尺寸）；新增依赖仅 golang.org/x/image v0.46.0；worker 披露 tidy 副作用 go-sql-driver/mysql indirect→direct（T2 orig.go 直接导入所致）；过程中发现并修复 applyAlpha 漏 A 通道真 bug，补 max-alpha=81 永久断言 |
| T2 | 存储层扩展（PresignWithTTL / orig / 负缓存） | P0 | `server/internal/storage/storage.go`、`local.go`、`s3.go`；新增 `server/internal/storage/orig.go` 及测试 | — | `cd server && go build ./... && go test ./...` | done | 2026-09-18 全绿：gofmt/vet 干净，build 通过，storage 包非缓存 `-count=1` ok（8 新用例 PASS：OrigPath/穿越锁/local HasOriginal/零值语义/S3 PresignWithTTL/负缓存三态/HasOriginalCached/S3 接线）；既有 S3 对齐整点用例未动仍绿；接口变更强制 fakeStorage（handler/testutil_test.go）与 mapStorage（service/cleanup_test.go）机械补 PresignWithTTL，已声明，无行为改动 |
| T3 | 契约冻结（无代码） | P0 | 无代码；契约全文落在本计划「接口契约」节 | — | —（评审确认契约文本） | done | 契约全文见本计划「接口契约」节（POST /download 两态响应、GET token 校验链、token 编码、包 API、存储接口变更），supervisor 批准确认 |
| T4 | 下发/下载闸门 | P0 | `server/internal/handler/media.go`；新增 `server/internal/handler/media_download.go`；`server/cmd/server/main.go`（路由注册与 Service 注入）；`server/internal/config/config.go`；对应 _test.go | T1、T2、T3 | `cd server && go build ./... && go test ./...` | done | 2026-09-18 全绿：build 通过、go test ./... 全 ok；泄漏面用例实测 PASS——①社区浏览者 POST /download→404（配对用例证明社区 GET 放行非空转）②token 过期/篡改/跨用户/坏格式/归属行已删→全 404 ③S3 paid+orig 302 TTL==360s；缓存头四态+304 不读体 PASS；POST 两态响应 PASS（实测 TTL 240-300s 区间）；严格归属 404 PASS；PUT 覆盖/Delete 补删 orig PASS；DerivePlan 失败 500 不降级 PASS；披露范围外必要改动 media_test.go 两条缓存头断言随行为变更更新（immutable→no-cache、wm- 前缀，feature 预期行为）；中间件横切面核对：POST 受 MaintenanceGate 影响、MediaAuth 对 POST 只认 Bearer（T7 已按此实现） |
| T5 | 生成落盘挂钩（fail-closed） | P0 | `server/internal/handler/ai.go`；`server/internal/service/aitask.go`；对应 _test.go | T4（验收后串行，避让 main.go 构造区） | `cd server && go build ./... && go test ./...` | done | 2026-09-18 全绿：`go test -count=1 ./...` exit 0，14 包 ok；fail-closed 实测 PASS（free+注入水印错误→502/0 媒体行/0 存储对象/退款恰 1 条；视频同构）；Save 失败补偿删 orig 0 残留 PASS；成功序（orig==原始字节、主对象==水印字节、mime==wmMime 含 webp→png）PASS；paid 跳过与 flag 关闭行为 PASS；单一汇聚点 saveGeneratedImage（ai.go:587）+ succeedTask 挂钩（aitask.go:209）；注入采用 SetWatermark setter（对齐 SetModeration 先例，避免波及禁改测试文件的构造调用）；退款幂等到注册点（MarkFailed 条件更新 + Refund 唯一索引）；worker 审计了中断轮遗留代码并修复 2 缺陷（aitask 死代码块、paid 夹具判档退化） |
| T6 | orig 连带清理 | P1 | `server/internal/service/cleanup.go`、`server/internal/service/deletion.go`；对应 _test.go | T2（与 T4/T5 并行） | `cd server && go build ./... && go test ./...` | done | 2026-09-18 全绿：`go test -count=1 ./internal/service/ ./internal/storage/ ./internal/handler/` 三包 ok；新增 4 用例 9/9 PASS（Reclaim 补删 orig 恰好、orig 删除失败 best-effort 主流程不受影响、注销 3 路径全删、注销 orig 失败不影响匿名化计数）；deletion.go 主删除失败新增 continue 跳过 orig（主流程行为不变）；未新增依赖、保护区零触碰 |
| T7 | 前端下载流程 | P1 | `web/src/services/api/media.ts`；`web/src/pages/assets/index.tsx`、`web/src/pages/image/index.tsx`、`web/src/pages/video/index.tsx` | T3（与 T4 并行） | `cd web && npm run typecheck && npm test` | done | 2026-09-18 全绿：typecheck 无输出、npm test 3 文件 6 用例全过；media.ts 新增 requestDownload（走统一 apiRequest）+ fetchMediaDownload（与 getMediaBlob 同凭据口径）；mediaFetch 参数化按 URL 请求，getMediaBlob 对外签名与行为零变化，canvas-export/asset-transfer/media-ingest 消费方不受影响；模块级运行时验证 3 用例过（POST 契约体/404→ApiError/相对路径拼接与 mediaUrl 一致）；隐式行为自查：401 刷新重放（client.ts:104-107）与 RequireAuth 重定向对两条请求均生效，画布导出链路零改动；已知取舍：下载按钮无 loading 防重（后端限流 60/h 兜底）、image/video 页失败提示沿用 getApiErrorMessage 三级文案（页内无 downloadFailed i18n key，locales 不在 write_scope） |
| T8 | 测试设计（test-planner） | P1 | `records/tests/infinite-canvas-media-watermark/`（用例库） | T3 | 用例库 smoke 集在评审门关闭前全部通过 | done | 2026-09-18 用例库写入：31 条（smoke 6 / e2e 7 / api 8 / boundary 10），8 项必须覆盖逐一落位，断言全部可观察（状态码/响应头/shasum/网络序列），边界矩阵含 9 类明确声明未覆盖等价类及原因，9 条流程疑点附建议口径；smoke 集 MW-SM-01~06 为评审门最小集；API-08/BD-02 标注「待 T5 落地后执行」 |

### 任务详情

**T1 水印渲染包（已完成）**：新增 `server/internal/watermark/`。API：`type Service struct`；`NewService(fontPath, text string)`；`(s) Image(src []byte, srcMime string) (out []byte, outMime string, err)`；`(s) Video(ctx, src []byte, srcMime string) ([]byte, error)`；`(s) TilePNG(width, height int) ([]byte, error)`；哨兵错误 `ErrUnsupportedFormat`；`Enabled()` 读 `WATERMARK_ENABLED`；`watermarkTimeout()` 读 `WATERMARK_TIMEOUT`（默认 120s）。图片支持 jpeg/png（原格式重编码，jpeg 质量 90）、webp（x/image/webp 解码 → PNG 输出，mime 变更为 image/png）；gif 及其它返回 `ErrUnsupportedFormat`。视频仅 video/mp4：Go 渲染整帧平铺水印 PNG + ffprobe 读帧尺寸 + ffmpeg overlay，超时 `WATERMARK_TIMEOUT`。水印文案 const 默认 `infinite-canvas`，`WATERMARK_TEXT` 覆盖（≤40 字符），`WATERMARK_FONT_PATH` 指定 CJK 字体。

**T2 存储层扩展（已完成）**：`Storage` 接口新增 `PresignWithTTL(ctx, path, ttl)`，现有 `Presign` 保留签名并退化为委托调用（s3 对齐整点语义不变）；新增 `orig.go`：`OrigPath(userID, storageKey) = "{uid}/orig/{冒号后id}"`（无扩展名，无冒号返回空串）、`HasOriginal`（local 直查 Stat；s3 负缓存仅缓存「确认不存在」负结论，进程内 map+RWMutex，非 NotFound 错误不写缓存）。

**T3 契约冻结（已完成）**：契约全文见「接口契约」节。

**T4 下发/下载闸门**（先于 T5，两者都要动 main.go 构造区）：
- `server/internal/handler/media.go`：Get/Head/serveLocal/redirectToPresigned 按归属者档位选字节——`DerivePlan(owner)`（`server/internal/service/quota.go:69`，注意是媒体行 user_id 而非浏览者）；plan!=paid → 直接服务现有对象（无需 Stat）；plan==paid → 负缓存/Stat orig，命中服务 orig，未命中服务现对象并记负缓存。缓存头分级：可变字节 `Cache-Control: private, no-cache` + ETag `wm-{checksum}`/`orig-{checksum}` + serveLocal 补 If-None-Match→304；稳定字节维持现状 immutable；S3 分支只换 Location 为 orig 的 PresignWithTTL(360s)，302 缓存头维持现状；Head 与 Get 同口径（出 orig 时 Content-Length 用 Stat 值）。Delete 补删 orig，best-effort。**补充（集成裁定）**：Put 覆盖写成功后 best-effort `storage.Delete(OrigPath(...))`——用户经 PUT 覆盖同 key 时旧 orig 成陈旧内容，覆盖后主对象即权威。
- 新增 `server/internal/handler/media_download.go`：POST /download 用**严格归属查询 user_id=当前用户 AND storage_key，禁走 findOwned/社区放行分支**，归属不符 404；HMAC 密钥 = HMAC-SHA256(key=JWTSecret, msg="ic-media-download-v1") 摘要；token = b64url(payload)+"."+b64url(sig)，payload = ver(1B)=1 ‖ exp(8B unix BE) ‖ uid(16B raw uuid) ‖ keyLen(2B BE) ‖ storageKey ‖ nonce(8B)；TTL 钳制 ≤300s；paid 无 orig 直接返回 media url。GET /api/media-download/:token：校验链 hmac.Equal → ver=1 → exp → token uid == 当前用户 → media_files 归属行仍存在 → 流式回流 orig（Content-Type/文件名嗅探、Content-Disposition: attachment、Cache-Control: private, no-store、nosniff），S3 302 PresignWithTTL(360s)；任何校验失败一律 404 防探测。
- `server/cmd/server/main.go`：POST 挂现有 media 组（`media.POST("/:storageKey/download", ...)`，参数段子路径与既有 GET/PUT/DELETE 同树不冲突）；GET 新组 /api/media-download 挂同款 MediaAuth+active+passwordGate；watermark.Service（`watermark.NewService(cfg 字体路径, cfg 文案)`）构造注入 media handler。
- `server/internal/config/config.go`：新增 `WATERMARK_ENABLED`（默认 false）、`WATERMARK_FONT_PATH`、`WATERMARK_TEXT`；`WATERMARK_ENABLED=true` 且字体缺失/不可读 → 启动报错 fail-fast。修正（批准时裁定）：`WATERMARK_TIMEOUT` 不进 config.go，由 watermark 包按仓库先例直接读 env，默认 120s。
- POST /download 挂限流（复用现有 limiter 模式，60 次/小时/用户）。
- **flag 语义（不得违背）**：`WATERMARK_ENABLED` 只控制生成时是否烧录（T5 消费）；下发闸门与 token 校验端点**永远在线**，不随 flag 关闭而下线——已存 orig 的付费用户若回退到水印主对象即属回归。flag 关闭时无新 orig 写入，闸门自然空转。
- 负缓存语义（T2 已定）：「确认不存在」随进程存活，无失效钩子——安全前提是 orig 仅在生成时写入、storageKey 每次生成为新 UUID，无 orig 的对象事后不会获得 orig。T4 只消费 `HasOriginal`，不新增失效逻辑，代码注释写明该前提。
- 单测必须含三个泄漏面锁死用例：①社区浏览者 POST 已发布作品的 /download → 404；②token 过期/篡改/跨用户 → 404；③S3 预签名 TTL ≤360s；另加缓存头分级用例（free→no-cache+wm-ETag、paid 有 orig→no-cache+orig-ETag、paid 无 orig→immutable、HEAD/GET 同口径）。

**T5 生成落盘挂钩**（T4 验收后串行）：`server/internal/handler/ai.go`（图片：审核通过后、Save 前插入——owner DerivePlan 为 free/sunset 时：watermark.Image 失败 → failRequest（既有退款路径，ai.go:1440-1453 一带）；成功 → storage.Put(orig) 失败 → 整单失败；Save 失败补偿 Delete(orig)。paid → 现状不变，不存 orig）；`server/internal/service/aitask.go`（succeedTask 同构：水印失败 → s.failTask 既有退款；写盘序同上）；`watermark.Enabled()` 为 false 时整段跳过（现状行为）。上传素材链路不挂钩。单测锁死 fail-closed：伪造 free 档 + 注入水印错误 → 断言无任何干净字节落盘/可下发路径。

**T6 orig 连带清理**：`server/internal/service/cleanup.go:129` 一带 Reclaim、`server/internal/service/deletion.go:135-170` 注销匿名化，补 `storage.Delete(origPath)` best-effort + error 日志。单测：删除媒体/注销后 orig 对象同步消失。注意 `cleanup_test.go` 的 mapStorage 已被 T2 机械补 PresignWithTTL，在其基础上扩展。

**T7 前端下载流程**：`web/src/services/api/media.ts` 新增 `requestDownload(storageKey)` → POST /api/media/{key}/download 返回 `{url, expiresAt}`；三个调用点改为「先 requestDownload → fetch(url, 带凭据) → blob → saveAs」：`web/src/pages/assets/index.tsx:154` downloadAsset、`web/src/pages/image/index.tsx:232` downloadImage、`web/src/pages/video/index.tsx:255` downloadVideo。canvas/project.tsx 与画布导出链路确认不改（下发层已按档位出字节）。检查：`npm run typecheck && npm test`。实现前先读 `web/src/services/api/` 的请求封装与拦截器（401/错误处理是全局机制），requestDownload 必须走同一封装。

**T8 测试设计**：test-planner 产出用例库 `records/tests/infinite-canvas-media-watermark/`，覆盖：T3 契约用例（响应两态）、三个泄漏面、缓存头分级（可变/稳定字节）、fail-closed 落盘序、格式矩阵（jpeg/png/webp/gif/mp4/字体缺失）、T6 清理、T7 三调用点；产出 smoke 集，「验证命令」节引用之；端到端用例须在评审门关闭前通过。

### 未决项

| 项 | 说明 | 处理建议 |
| --- | --- | --- |
| 边界值默认值（已随批准生效） | WATERMARK_TIMEOUT=120s、token TTL≤300s、S3 预签名 360s、限流 60 次/小时/用户、文案 ≤40 字符、jpeg q90、平铺参数（字号 max(16,帧宽/28)/间距 4×/-30°/alpha 0.32）、WATERMARK_ENABLED 默认 false | 已按用户「开始开发」指令采用推荐值；最终报告向用户逐项列出，可调 |
| ffmpeg/ffprobe 与 CJK 字体 | 部署前提：PATH 内 ffmpeg/ffprobe（ffmpeg 需含 libx264）+ WATERMARK_FONT_PATH 指向可用 CJK 字体文件；flag=true 且字体缺失启动 fail-fast | 写入部署前置清单 |
| orig 孤儿长期策略 | T6 双入口补删 best-effort 之外的残余孤儿无自动回收 | 本期不做；后续以存储生命周期策略或定期扫描待办承接 |
| scale2ref 弃用 | ffmpeg 8.1 标记 deprecated（仍可用）；未来移除会导致视频水印 fail-closed 失败 | 已知部署环境风险，观察 |

## 接口与共享机制

| 机制 | 注册点 | 消费点 | 说明 |
| --- | --- | --- | --- |
| watermark.Service（NewService/Image/Video/TilePNG/ErrUnsupportedFormat/Enabled/watermarkTimeout） | T1 `server/internal/watermark/`（已落地） | T5 生成挂钩、T4 构造注入 | 图片 jpeg/png 原格式重编码（jpeg q90）、webp→PNG（mime 变 image/png）、gif/其它 ErrUnsupportedFormat；视频仅 mp4 |
| Storage.PresignWithTTL(ctx, path, ttl) | T2 storage.go（已落地） | T4 media.go S3 分支、media_download.go | 现有 Presign 委托调用，老调用点不动 |
| OrigPath / HasOriginal / S3 负缓存 | T2 orig.go（已落地） | T4 选字节、T5 落盘、T6 清理 | 负缓存仅缓存「无 orig」负结论，进程内 map+RWMutex；local 驱动直查 |
| QuotaService.DerivePlan（既有） | `server/internal/service/quota.go:69` | T4 下发选字节、T5 生成挂钩 | 一律取媒体行 user_id（归属者），绝不取浏览者 |
| POST /download + GET /api/media-download/:token + HMAC token | T4 media_download.go；main.go 注册 | T7 requestDownload | 契约全文见「接口契约」节 |
| WATERMARK_ENABLED / WATERMARK_FONT_PATH / WATERMARK_TEXT | T4 config.go | T5 开关、T4 构造注入 | ENABLED 默认 false；启用时字体缺失 fail-fast |
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

### watermark 包 API 签名（T1 已实现）

```go
type Service struct{ /* fontPath, text */ }
func NewService(fontPath, text string) *Service
func (s *Service) Image(src []byte, srcMime string) (out []byte, outMime string, err error)
func (s *Service) Video(ctx context.Context, src []byte, srcMime string) ([]byte, error)
func (s *Service) TilePNG(width, height int) ([]byte, error)
var ErrUnsupportedFormat = errors.New(...)
func Enabled() bool          // WATERMARK_ENABLED，"1"/"true"/"TRUE"，默认关
func watermarkTimeout() time.Duration // WATERMARK_TIMEOUT，默认 120s（包内私有）
```

### 存储接口变更（T2 已实现）

- `Storage` 接口新增：`PresignWithTTL(ctx context.Context, path string, ttl time.Duration) (Presigned, error)`；现有 `Presign(ctx, path)` 保留并退化为委托调用。
- `orig.go`：`OrigPath(userID, storageKey string) string`（`"{uid}/orig/{冒号后id}"`，无扩展名）、`HasOriginal(ctx, stor, uid, key)`；S3 负缓存仅缓存「无 orig」负结论。

### 边界值与默认值

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
| A 生成落盘 | 状态冲突：orig 已存在/负缓存残留 | Put 覆盖写；下发时 Stat/HasOriginal 校正 | 覆盖写 + 负缓存自愈（进程重启清空） | N/A——无重试语义 | 同上 | 短暂旧水印版，随后自动变干净 |
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
| S3 | 历史干净件：上线前 free 用户已生成的素材仍是干净版，本期不追溯回填 | 已知限制，接受；免费档保留期仅 7 天，自然窗口极短；后续如需回填另立待办 |
| S3 | 视频收敛延迟：ffmpeg 转码耗时/任务积压 | WATERMARK_TIMEOUT=120s + preset veryfast + 灰度观察 watermark_failed |
| S4 | HEAD 不一致与 EXIF：Head/Get 口径不一；EXIF 元数据随原图泄漏 | T4 Head 与 Get 同口径单测；EXIF 由重编码剥离，T1 测试覆盖 |

## 验证命令

| 范围 | 命令 / 步骤 | 预期 |
| --- | --- | --- |
| T4/T5/T6（server） | `cd server && go build ./... && go test ./...` | 全绿；T4 含三个泄漏面用例与缓存头分级用例，T5 含 fail-closed 用例，结果摘要写回任务表 evidence 列 |
| T7（web） | `cd web && npm run typecheck && npm test` | 全绿；画布导出链路无改动 |
| T8 smoke 集 | 运行 `records/tests/infinite-canvas-media-watermark/` 用例库的 smoke 子集 | 评审门关闭前全部通过，含端到端：flag=true 下免费账号生成→展示/下载均水印；付费（或升级）账号→POST /download 签发→GET token 流式取干净件；过期/篡改/跨用户 token→404 |
| 集成 | supervisor 全量检查 + git 变更面比对 write_scope | 起点=基线（19f5cd4 起）+ 保护区；仅允许新增/修改任务表所列文件，保护区零触碰 |
| 真库验证 | 真实 MySQL 跑一次迁移 + 启动 | 迁移与启动成功（无 schema 变更仍执行） |
| 环境前提（测试替身） | ffmpeg/ffprobe 在 PATH；CJK 字体 fixture；样本夹具 jpeg/png/webp/gif/mp4 各一 | 缺失时视频用例明确 skip 或 fail，不允许静默假绿 |

**评审门（material 变更，独立 reviewer，作者不自审）**：重点核对三个泄漏面单测、缓存头分级、fail-closed、保护区零触碰；端到端用例先于评审门通过。
