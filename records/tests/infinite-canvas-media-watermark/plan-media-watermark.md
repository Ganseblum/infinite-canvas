---
test_plan_id: TP-20260918-001
title: 媒体水印与干净原件安全下发用例库
project: infinite-canvas
feature: media-watermark
target_revision: 19f5cd4270ec130a22fb0d06c67f1efeb492b2c0
credentials: 测试环境临时账号——各账号的会话凭据（MediaAuth 既有机制）由执行环境注入，本文件不出现任何凭据值
case_count: 31
test_version: 1
status: draft
updated_at: 2026-09-18T00:00:00Z
---

# TP-20260918-001 媒体水印与干净原件安全下发用例库

- 权威输入：`records/plans/media-watermark.md` v2（T3 冻结契约、异常矩阵、边界值表、风险清单）。T4（`server/internal/handler/media_download.go`）设计时点尚未落地，本用例库一律以冻结契约为准，不依赖实现细节。
- 文件名说明：本文件按 supervisor 指定路径 `plan-media-watermark.md` 归档，ID 为 `TP-20260918-001`。
- 结构说明：仓库当前不存在 `schemas/test-plan.schema.json` 与 `records/tests/README.md`，本文按角色契约的固定三节（`## 用例` / `## 边界覆盖` / `## 流程疑点`）编写；schema 落地后需回验。

## 环境前提与约定（全部用例共用）

### 变量表（环境值与账号一律用变量，不得写进步骤）

| 变量 | 含义 |
| --- | --- |
| `{BASE_URL}` | 测试环境 API 基址（前端 API_BASE_URL 同源值） |
| `{FREE}` / `{PAID}` / `{USER_B}` | free 档 / paid 档 / 第二测试账号（测试环境临时账号，凭据执行时注入） |
| `{FREE_KEY}` | free 账号生成素材的 storageKey |
| `{PAID_ORIG_KEY}` | paid 账号生成、已存 orig 的素材 storageKey |
| `{PAID_NOORIG_KEY}` | paid 账号无 orig 的素材 storageKey（如上传件） |
| `{AUDIO_KEY}` | 免费档音频素材 storageKey |
| `{COMMUNITY_KEY}` | 他人已发布社区作品的 storageKey |
| `{TOKEN}` | POST /download 响应返回的取件 token |
| `{UID}` | 账号用户 id（local 驱动核对文件系统用） |

### 环境前提

1. `WATERMARK_ENABLED` 可切换（改动需重启 server 生效；`Enabled()` 进程内只读一次）。
2. `WATERMARK_FONT_PATH` 指向测试字体 fixture；ffmpeg/ffprobe 在 PATH（视频用例前提）。
3. 存储驱动 local 或 S3：标注「local 文件系统核对」的步骤仅在 local 驱动执行；标注「S3 环境」的步骤在 local 环境显式 skip 并注明原因，不允许静默假绿。
4. 样本夹具：jpeg / png / webp / gif / mp4 / 无水印音频各一。
5. 「水印可见」的判定方式：图片查看器 / 播放器目视平铺重复水印文字（默认 `infinite-canvas`）；「干净原件」判定：同画面无平铺水印文字，必要时 `shasum -a 256` 与水印版哈希比对不同。以上均为不读代码的可操作断言。
6. POST /download、GET /api/media-download/:token 的会话凭据按 MediaAuth 既有机制注入（与现有 media 组一致），写路径类请求带 Bearer。

### 会话与执行顺序约束

- 隔离执行或置于共享会话最后：MW-API-07（注销销毁账号）、MW-E2E-01（升级付费变更账号档位）、MW-BD-09（专用账号打满限流配额）。
- 其余用例可共享登录会话；无「必须登出」类用例。

### 执行方式标注约定

- `curl`：按步骤给出的 HTTP 请求直接执行，状态码用 `curl -s -o /dev/null -w "%{http_code}"` 观察。
- `go test`：给出包名/命令，以测试输出为观察对象。
- `supervisor 浏览器执行`：需人工/主代理在浏览器中操作。
- `Playwright 可脚本化`：可脚本化的浏览器用例（含网络拦截、请求序列断言）。
- UI 锚点说明：项目未提供 test-id → alias 注册表与用例引擎，本用例库以「页面 + 按钮 i18n 文案键 + 可见文案」为锚点；项目建立注册表后应迁移为别名引用。

## 用例

用例注册表：

| 用例 ID | 级别 | 优先级 | 标题 |
| --- | --- | --- | --- |
| MW-SM-01 | smoke | P0 | flag=true 免费生成图片：展示与下载均为水印版 |
| MW-SM-02 | smoke | P0 | 免费账号申请下载自有素材：返回 media url + expiresAt null |
| MW-SM-03 | smoke | P0 | paid+orig 签发取件：token url + RFC3339，取到干净原件 |
| MW-SM-04 | smoke | P0 | token 过期/篡改/跨用户一律 404（泄漏面②） |
| MW-SM-05 | smoke | P0 | 社区浏览者申请下载已发布作品 404（泄漏面①） |
| MW-SM-06 | smoke | P0 | 升级付费后同素材展示与下载自动变干净 |
| MW-E2E-01 | e2e | P0 | 端到端主链路：生成→水印→升级→干净→签发→取件→过期 |
| MW-E2E-02 | e2e | P1 | 免费生成 mp4 视频：展示与下载均为水印版 |
| MW-E2E-03 | e2e | P1 | 三页下载按钮走「申请→取件」两步网络序列 |
| MW-E2E-04 | e2e | P1 | 免费上传素材不打水印 |
| MW-E2E-05 | e2e | P1 | flag=false 回滚语义：新产物不打水印、闸门仍在线 |
| MW-E2E-06 | e2e | P2 | 画布导出链路与档位一致 |
| MW-E2E-07 | e2e | P2 | 申请/取件失败时页面出现下载失败提示且不保存文件 |
| MW-API-01 | api | P1 | paid 无 orig 申请下载：回退 media url + null |
| MW-API-02 | api | P0 | 缓存头分级四态 + If-None-Match 304 |
| MW-API-03 | api | P1 | HEAD 与 GET 同口径（paid+orig 含 Content-Length） |
| MW-API-04 | api | P1 | 泄漏面③：S3 预签名 TTL ≤360s（下发与取件两处） |
| MW-API-05 | api | P0 | GET token 成功响应的头与内容口径 |
| MW-API-06 | api | P1 | 删除媒体后 orig 同步消失、旧 token 404 |
| MW-API-07 | api | P1 | 注销账号后 orig 同步消失 |
| MW-API-08 | api | P0 | fail-closed 服务端锁死：水印失败无干净字节可取 |
| MW-BD-01 | boundary | P1 | 图片格式矩阵成功面（jpeg/png/webp）与 TilePNG 像素断言 |
| MW-BD-02 | boundary | P0 | gif/非 mp4 → ErrUnsupportedFormat → 生成 fail-closed |
| MW-BD-03 | boundary | P1 | flag=true 且字体缺失 → 启动 fail-fast |
| MW-BD-04 | boundary | P1 | mp4 真实转码水印成功 + ffprobe 校验 |
| MW-BD-05 | boundary | P1 | storageKey 非法/空/特殊字符 → 不签发 |
| MW-BD-06 | boundary | P2 | 重复签发与 token 重放：各自有效、过期后可重申 |
| MW-BD-07 | boundary | P1 | PUT 覆盖同 key 后 orig 清除、旧 token 不再出旧原件 |
| MW-BD-08 | boundary | P2 | 未登录/过期会话：申请与取件均 401 |
| MW-BD-09 | boundary | P2 | 限流：第 61 次/小时 POST /download 被拒 |
| MW-BD-10 | boundary | P2 | 超长 WATERMARK_TEXT（>40 字符）不静默 |

### smoke 集（评审门关闭前必须全绿的最小集）

运行顺序即下表顺序；SM-04 的过期等待与 SM-03 的签发共用账号（隔离账号，勿与他用例混用会话状态）。

| 顺序 | 用例 | 执行方式 | 通过判据 |
| --- | --- | --- | --- |
| 1 | MW-SM-01 | supervisor 浏览器执行 | 展示与下载文件均可见平铺水印 |
| 2 | MW-SM-02 | curl | 200；`url` 为 `/api/media/` 形态；`expiresAt` 为 null |
| 3 | MW-SM-03 | curl | 200；token url + RFC3339；GET token 200 且内容无水印 |
| 4 | MW-SM-04 | curl | 篡改/跨用户/过期三种 token 均 404 |
| 5 | MW-SM-05 | curl | 浏览者 POST /download 404，与不存在 key 同口径 |
| 6 | MW-SM-06 | supervisor 浏览器执行 + curl | 升级后展示与下载变干净，POST /download 签发 token url |

### MW-SM-01 flag=true 免费生成图片：展示与下载均为水印版

- 级别 smoke / P0；执行方式：supervisor 浏览器执行
- 前置条件：`WATERMARK_ENABLED=true` 的测试环境；`{FREE}` 已登录；AI 图片生成可用；本账号在本环境 flag=true 期间生成（保证 orig 已写，供 MW-SM-06 复用）。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 打开图片生成页（图片页，图片生成入口） | 任一提示词，生成 1 张 | 生成成功，结果区出现图片卡片 |
| 观察结果区图片 | — | 图片上可见平铺重复的水印文字（默认 `infinite-canvas`） |
| 点击该卡片「下载」按钮（i18n `common.download`） | — | 浏览器保存文件成功 |
| 用图片查看器打开下载文件 | — | 文件内容同样可见平铺水印文字，与展示图一致为水印版 |
| 本地执行 `shasum -a 256 <下载文件>` | — | 记录哈希值，供 MW-SM-06 步骤对比 |

### MW-SM-02 免费账号申请下载自有素材：返回 media url + expiresAt null

- 级别 smoke / P0；执行方式：curl（T3 两态契约之 free 态）
- 前置条件：`{FREE}` 会话可用；`{FREE_KEY}` 为该账号生成素材。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| POST `{BASE_URL}/api/media/{FREE_KEY}/download`（`{FREE}` 会话） | — | HTTP 200，响应体 JSON 含 `url` 与 `expiresAt` 两个字段 |
| 检查字段值 | — | `url` 以 `/api/media/` 开头（即 media 下发 URL 形态）；`expiresAt` 为 JSON `null` |
| GET 返回的 `url`（同会话） | — | HTTP 200，返回内容为该素材的水印版（目视含水印） |

### MW-SM-03 paid+orig 签发取件：token url + RFC3339，取到干净原件

- 级别 smoke / P0；执行方式：curl（T3 两态契约之 paid+orig 态）
- 前置条件：`{PAID}` 会话可用；`{PAID_ORIG_KEY}` 为该账号在 flag=true 期间生成的素材（orig 已存）。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| POST `{BASE_URL}/api/media/{PAID_ORIG_KEY}/download`（`{PAID}` 会话） | — | HTTP 200；`url` 以 `/api/media-download/` 开头；`expiresAt` 为 RFC3339 字符串 |
| 解析 `expiresAt` 与当前时间差 | — | 差值 > 0 且 ≤ 300 秒（TTL 钳制上限） |
| GET 返回的取件 url（同会话） | — | HTTP 200 |
| 保存响应体为文件并目视 | — | 内容为干净原件：无平铺水印文字；`shasum -a 256` 与同素材水印版（如 MW-SM-01 记录）不同 |

### MW-SM-04 token 过期/篡改/跨用户一律 404（泄漏面②）

- 级别 smoke / P0；执行方式：curl
- 前置条件：沿用 MW-SM-03 签发的 token（记 `T`）；第二账号 `{USER_B}`（paid 或 free 均可）；过期态需实时等待 ≤300 秒（见流程疑点 Q3）。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 将 `T` 的 payload 段任一字符替换后 GET `/api/media-download/{篡改 token}` | `{PAID}` 会话 | HTTP 404 |
| 用 `{USER_B}` 会话 GET `/api/media-download/{T}`（跨用户） | `{USER_B}` 会话 | HTTP 404 |
| 在 `expiresAt` 时刻之后重放 GET `/api/media-download/{T}` | `{PAID}` 会话 | HTTP 404 |
| 三个 404 响应体互相比对 | — | 错误结构与「资源不存在」一致（同一错误码），不区分具体失败原因（防探测） |

### MW-SM-05 社区浏览者申请下载已发布作品 404（泄漏面①）

- 级别 smoke / P0；执行方式：curl
- 前置条件：账号 A 已发布社区作品，`{COMMUNITY_KEY}` 为其内容 storageKey；`{USER_B}` 为非作者浏览者。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| GET `{BASE_URL}/api/media/{COMMUNITY_KEY}`（`{USER_B}` 会话） | — | HTTP 200（社区只读放行为现状行为，作为对照） |
| POST `{BASE_URL}/api/media/{COMMUNITY_KEY}/download`（`{USER_B}` 会话） | — | HTTP 404 |
| POST `{BASE_URL}/api/media/{不存在的 key}/download`（`{USER_B}` 会话） | — | HTTP 404，响应结构与上一步一致（防探测同口径） |

### MW-SM-06 升级付费后同素材展示与下载自动变干净

- 级别 smoke / P0；执行方式：supervisor 浏览器执行 + curl
- 前置条件：延续 MW-SM-01 的账号与素材（该账号生成于 flag=true 期间，orig 已存；已记录水印版哈希）；测试环境可将该账号档位改为 paid。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 将该账号升级为 paid | 测试环境档位变更 | 生效（页面账号信息显示付费档） |
| 刷新素材页查看 MW-SM-01 素材 | — | 图片展示已无平铺水印（下发层按归属者档位出干净原件） |
| 点击「下载」并打开文件 | — | 文件无水印；`shasum -a 256` 与 MW-SM-01 记录的哈希不同 |
| POST `{BASE_URL}/api/media/{FREE_KEY}/download`（curl，paid 会话） | — | 200；`url` 以 `/api/media-download/` 开头；`expiresAt` 为 RFC3339 |

### MW-E2E-01 端到端主链路：生成→水印→升级→干净→签发→取件→过期

- 级别 e2e / P0；执行方式：supervisor 浏览器执行（步骤 4–6 可用 curl）；隔离账号
- 前置条件：`WATERMARK_ENABLED=true`；全新 free 临时账号；本用例串联各 smoke 断言为一条时间线，执行中账号状态单向演进（free→paid），不可与其它用例共用。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 免费账号生成图片并查看展示、下载文件 | 任一提示词 | 展示与下载文件均可见平铺水印（同 MW-SM-01 判据） |
| 将该账号升级为 paid 后刷新查看同素材 | — | 展示与下载均变为干净版（同 MW-SM-06 判据） |
| POST `/api/media/{该素材 key}/download`（curl） | — | 200；token url + RFC3339 `expiresAt`（≤300s） |
| GET 返回的取件 url | — | 200；响应体为干净原件（无水印） |
| 等待 `expiresAt` 过后重放该取件 url | — | HTTP 404 |
| 重新 POST `/download` | — | 200，签发新的可用 token（nonce 不同、url 不同） |

### MW-E2E-02 免费生成 mp4 视频：展示与下载均为水印版

- 级别 e2e / P1；执行方式：supervisor 浏览器执行
- 前置条件：`WATERMARK_ENABLED=true`；ffmpeg/ffprobe 在 PATH；`{FREE}` 已登录；视频生成可用。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 视频页发起视频生成 | 任一提示词 | 生成成功（允许排队等待完成） |
| 在视频页结果区播放视频 | — | 画面可见平铺水印 |
| 点击「下载」（i18n `common.download`）并播放下载文件 | — | 下载的 mp4 画面同样含水印 |
| 若环境缺 ffmpeg：观察生成结果 | — | 生成失败并出现退款/失败提示（fail-closed），此时以「失败提示出现」为通过判据；两种环境结果按实际二选一记录，不允许静默 |

### MW-E2E-03 三页下载按钮走「申请→取件」两步网络序列

- 级别 e2e / P1；执行方式：Playwright 可脚本化（supervisor 浏览器执行时用 DevTools Network 观察）
- 前置条件：任一账号素材页有图片素材、图片页有生成结果、视频页有生成结果；可观察网络请求。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 素材页点击素材卡「下载」（i18n `assets.downloadImage` / `assets.downloadVideo` 入口） | — | Network 先出现 `POST /api/media/{key}/download`（200），随后出现对其返回 url 的 GET；文件保存成功 |
| 图片页对生成结果点「下载」 | — | 同上两步网络序列；文件保存成功 |
| 视频页对生成结果点「下载」 | — | 同上两步网络序列；文件保存成功 |

### MW-E2E-04 免费上传素材不打水印

- 级别 e2e / P1；执行方式：supervisor 浏览器执行
- 前置条件：`WATERMARK_ENABLED=true`；`{FREE}` 已登录；本地夹具：无水印 jpeg。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 素材页上传该 jpeg | — | 上传成功，素材列表出现该素材 |
| 查看该素材展示 | — | 图片无平铺水印（上传链路不挂钩） |
| 点击「下载」并打开文件 | — | 下载文件同样无水印 |

### MW-E2E-05 flag=false 回滚语义：新产物不打水印、闸门仍在线

- 级别 e2e / P1；执行方式：supervisor 浏览器执行（含服务重启操作）
- 前置条件：可将 `WATERMARK_ENABLED` 改为 false 并重启的测试环境；沿用 MW-E2E-01 的 paid 账号与其 orig 素材（闸门在线的对照物）。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 关闭 `WATERMARK_ENABLED` 并重启 server | — | 服务健康（登录与页面可用） |
| free 账号新生成图片 | 任一提示词 | 展示与下载均无水印（现状行为，flag 只控生成） |
| paid 账号查看 MW-E2E-01 的 orig 素材 | — | 展示仍为干净原件版（下发闸门不随 flag 下线） |
| curl：paid 会话 POST `/download` 该素材 | — | 200 且仍签发 token url（闸门永远在线，不得回退水印主对象） |

### MW-E2E-06 画布导出链路与档位一致

- 级别 e2e / P2；执行方式：supervisor 浏览器执行
- 前置条件：`WATERMARK_ENABLED=true`；`{FREE}` 画布项目中含一张生成图片节点。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 打开画布项目并执行导出 | — | 导出成功，文件保存 |
| 打开导出文件 | — | 免费档导出内容含水印（与素材页下载档位一致；导出链路本身行为不变） |

### MW-E2E-07 申请/取件失败时页面出现下载失败提示且不保存文件

- 级别 e2e / P2；执行方式：Playwright 可脚本化（supervisor 浏览器执行时用 DevTools 手工拦截）
- 前置条件：浏览器可拦截/中止指定请求；素材页存在可下载素材。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 拦截 `POST /api/media/*/download` 使其网络失败/500 后点击「下载」 | — | 页面出现下载失败 toast（i18n `assets.downloadFailed` 文案），浏览器未保存文件 |
| 取消拦截后再次点击「下载」 | — | 下载成功，文件保存 |

### MW-API-01 paid 无 orig 申请下载：回退 media url + null

- 级别 api / P1；执行方式：curl（T3 两态契约之 paid 无 orig 态）
- 前置条件：`{PAID}` 会话；`{PAID_NOORIG_KEY}` 为该账号上传素材（未走生成链路，无 orig）。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| POST `{BASE_URL}/api/media/{PAID_NOORIG_KEY}/download` | — | HTTP 200 |
| 检查响应字段 | — | `url` 以 `/api/media/` 开头；`expiresAt` 为 `null`（与 free 态同响应形态） |

### MW-API-02 缓存头分级四态 + If-None-Match 304

- 级别 api / P0；执行方式：curl（local 驱动环境；S3 专属断言见步骤 6）
- 前置条件：local 驱动；`{FREE_KEY}`（free 生成图片）、`{PAID_ORIG_KEY}`（paid 有 orig）、`{PAID_NOORIG_KEY}`（paid 无 orig 上传件）、`{AUDIO_KEY}`（免费档音频）四类素材就绪。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| GET `{BASE_URL}/api/media/{FREE_KEY}`（free 会话） | — | 200；`Cache-Control: private, no-cache`；`ETag` 以 `wm-` 开头 |
| GET `{BASE_URL}/api/media/{PAID_ORIG_KEY}`（paid 会话） | — | 200；`Cache-Control: private, no-cache`；`ETag` 以 `orig-` 开头；内容为干净原件（无水印） |
| GET `{BASE_URL}/api/media/{PAID_NOORIG_KEY}`（paid 会话） | — | 200；`Cache-Control` 含 `immutable`（稳定字节维持现状头） |
| GET `{BASE_URL}/api/media/{AUDIO_KEY}` | — | 200；`Cache-Control` 含 `immutable`（音频不受水印影响） |
| 再次 GET `{FREE_KEY}` 并带请求头 `If-None-Match: <步骤1 的 ETag>` | — | HTTP 304（local 驱动 304 生效） |
| （仅 S3 环境）重复步骤 1–4 | — | 302 到对应 orig/现对象的预签名 URL；302 缓存头维持现状口径。local 环境执行时此步标注 skip 并注明驱动原因 |

### MW-API-03 HEAD 与 GET 同口径（paid+orig 含 Content-Length）

- 级别 api / P1；执行方式：curl
- 前置条件：`{PAID}` 会话；`{PAID_ORIG_KEY}` 就绪。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| HEAD `{BASE_URL}/api/media/{PAID_ORIG_KEY}`（`-I`） | — | 200；记录 `Content-Length`、`ETag`、`Cache-Control`（`X-Checksum` 取值一并记录，口径见流程疑点 Q8） |
| GET 同 url 并保存 body 后 `wc -c` | — | 200；`ETag` 与 `Cache-Control` 与 HEAD 一致；`Content-Length` 与 HEAD 一致且等于实际 body 字节数（orig 尺寸） |

### MW-API-04 泄漏面③：S3 预签名 TTL ≤360s（下发与取件两处）

- 级别 api / P1；执行方式：curl（仅 S3 驱动环境；local 环境整体标注 skip 并注明原因，不允许静默假绿）
- 前置条件：S3 驱动测试环境；`{PAID_ORIG_KEY}` 就绪并已签发 token。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| GET `{BASE_URL}/api/media/{PAID_ORIG_KEY}` 不跟随重定向（`curl -sI`） | — | 302；解析 `Location` 的签名过期参数，与当前时刻差 ≤ 360 秒 |
| GET `/api/media-download/{TOKEN}` 不跟随重定向 | — | 302；`Location` 的签名过期参数同样 ≤ 360 秒 |

### MW-API-05 GET token 成功响应的头与内容口径

- 级别 api / P0；执行方式：curl
- 前置条件：沿用 MW-SM-03 签发的有效 token；orig 为 jpeg 或 png 生成原件。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| GET `/api/media-download/{TOKEN}`（local 驱动） | — | 200 |
| 检查响应头 | — | `Content-Disposition` 以 `attachment` 开头；`Cache-Control: private, no-store`；`X-Content-Type-Options: nosniff` |
| 检查 `Content-Type` 与 body | — | `Content-Type` 与 orig 内容一致（按内容嗅探；记录实际值）；body 字节数 > 0 且可被图片查看器打开 |
| （仅 S3 环境）同请求 | — | 302 到预签名 URL（TTL 断言见 MW-API-04），local 步骤与 S3 步骤按驱动二选一执行并记录 |

### MW-API-06 删除媒体后 orig 同步消失、旧 token 404

- 级别 api / P1；执行方式：curl + local 文件系统核对
- 前置条件：`{PAID}` 会话；`{PAID_ORIG_KEY}` 就绪（local 驱动）；记录该账号 `{UID}`。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| POST `/download` 签发并记 token `T1` | — | 200 |
| DELETE `{BASE_URL}/api/media/{PAID_ORIG_KEY}` | — | 2xx（现状 204） |
| local：检查存储根目录 `{UID}/orig/{storageKey 冒号后 id}` | — | 文件已不存在（orig 连带删除） |
| GET `/api/media-download/{T1}` | — | HTTP 404（媒体归属行已删，校验链失败） |
| 再次 POST `/api/media/{PAID_ORIG_KEY}/download` | — | HTTP 404 |

### MW-API-07 注销账号后 orig 同步消失

- 级别 api / P1；执行方式：浏览器注销操作 + local 文件系统核对；隔离执行（账号销毁不可复用）
- 前置条件：专用临时账号在 flag=true 期间生成过素材（orig 已存）；local 驱动；记录 `{UID}`。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| local：确认存储根目录 `{UID}/orig/` 下存在对象 | — | 目录非空（基线） |
| 执行账号注销（设置页注销入口或既有注销 API） | — | 注销流程完成 |
| local：检查 `{UID}/orig/` | — | orig 对象已消失（媒体主对象按既有注销口径同步核对） |
| 用该账号原会话调用任一需鉴权接口 | — | HTTP 401（账号已失效） |

### MW-API-08 fail-closed 服务端锁死：水印失败无干净字节可取

- 级别 api / P0；执行方式：go test（依赖 T5 落地；T5 验收前本用例标注「待实现」，评审门不得在「待实现」状态下关闭）

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 执行 `cd server && go test ./... -count=1` | — | 全部 PASS，0 FAIL |
| 核对输出中存在 T5 fail-closed 用例（注入水印错误 → 断言无任何干净字节落盘/可下发路径） | `-v` 输出 | 用例存在且 PASS；将实际用例名记入执行 evidence |
| 核对输出中存在 T4 三个泄漏面用例（社区浏览者 404 / token 失效 404 / S3 TTL≤360s） | `-v` 输出 | 三类用例存在且 PASS；记录实际用例名 |
| 若本机缺 ffmpeg 导致视频用例 skip | `-v` 输出 | skip 数量与原因可见，视频相关断言不得计为通过（显式声明） |

### MW-BD-01 图片格式矩阵成功面（jpeg/png/webp）与 TilePNG 像素断言

- 级别 boundary / P1；执行方式：go test

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 执行 `cd server && go test ./internal/watermark/ -count=1 -v` | — | 全部 PASS：jpeg/png 原格式重编码、webp 解码转 PNG（mime 变更 image/png）、TilePNG 像素断言（水印后字节必变）全部通过；0 FAIL |
| 核对 skip 数 | `-v` 输出 | 0 skip（图片用例不依赖 ffmpeg；若有 skip 需注明原因） |

### MW-BD-02 gif/非 mp4 → ErrUnsupportedFormat → 生成 fail-closed

- 级别 boundary / P0；执行方式：go test（依赖 T5 落地；UI 不可稳定触发「生成产物为 gif」，故不设 UI 用例——降级声明）
- 前置条件：T5 已落地。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 执行 `cd server && go test ./internal/watermark/ -count=1 -v` | — | gif 输入返回 `ErrUnsupportedFormat` 的用例 PASS |
| 执行 `cd server && go test ./... -count=1`（含 T5） | — | 存在「不支持格式 → 生成失败并退款、无干净字节落盘」用例且 PASS（实际用例名记入 evidence） |

### MW-BD-03 flag=true 且字体缺失 → 启动 fail-fast

- 级别 boundary / P1；执行方式：服务启动命令观察

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 以 `WATERMARK_ENABLED=true`、`WATERMARK_FONT_PATH=/nonexistent/font.ttf` 启动 server | — | 进程启动失败退出（非 0 退出码），错误日志指向字体读取失败 |
| 以 `WATERMARK_ENABLED=false`、同字体路径重启 | — | 启动成功（fail-fast 仅在 flag=true 时触发） |

### MW-BD-04 mp4 真实转码水印成功 + ffprobe 校验

- 级别 boundary / P1；执行方式：go test（ffmpeg 前提）

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 确认 `ffmpeg -version`、`ffprobe -version` 可执行 | — | 版本号输出正常（环境基线） |
| 执行 `cd server && go test ./internal/watermark/ -count=1 -v` | — | 视频转码用例 PASS：真实转码完成且 ffprobe 校验输出尺寸；无 ffmpeg 时输出显式 skip 及原因，不得计为通过 |

### MW-BD-05 storageKey 非法/空/特殊字符 → 不签发

- 级别 boundary / P1；执行方式：curl
- 前置条件：任一有效会话。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| POST `{BASE_URL}/api/media/image:../evil/download` | — | 非 200（预期 400，与组内 GET/PUT/DELETE 同口径，见流程疑点 Q6；记录实际状态码），响应体无 `url` 字段 |
| POST `{BASE_URL}/api/media//download`（空 key） | — | 非 200（路由不匹配），响应体无 `url` 字段 |
| POST `{BASE_URL}/api/media/image:%2e%2e%2f/download`（编码穿越） | — | 非 200，响应体无 `url` 字段；不产生任何可取件 URL |

### MW-BD-06 重复签发与 token 重放：各自有效、过期后可重申

- 级别 boundary / P2；执行方式：curl
- 前置条件：`{PAID}` 会话；`{PAID_ORIG_KEY}` 就绪。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 连续 3 次 POST `/download` | — | 3 次均 200，三个 `url` 互不相同（nonce 保证 token 唯一） |
| 在各自 `expiresAt` 前分别 GET 三个取件 url | — | 3 次均 200（旧 token 在 exp 前持续有效） |
| 任一 token 过期后重放、并重新 POST `/download` | — | 过期 token 404；重新签发 200 且新 token 可取件 |

### MW-BD-07 PUT 覆盖同 key 后 orig 清除、旧 token 不再出旧原件

- 级别 boundary / P1；执行方式：curl
- 前置条件：`{PAID}` 会话；`{PAID_ORIG_KEY}` 就绪；本地夹具新 jpeg 一张（内容与原素材不同）；先签发 token `T0`。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| PUT `{BASE_URL}/api/media/{PAID_ORIG_KEY}`（body=新 jpeg，`Content-Type: image/jpeg`） | — | 2xx（现状 201） |
| GET 同 key | — | 200；内容为新上传内容（`ETag` 与覆盖前不同，无 `orig-` 前缀） |
| POST `/download` 同 key | — | 200；`expiresAt` 为 `null`（覆盖后主对象即权威，orig 已清） |
| GET `/api/media-download/{T0}`（覆盖前签发） | — | 非 200（建议口径 404，见流程疑点 Q7）；无论何种状态码，响应体不得为被删 orig 的旧字节 |

### MW-BD-08 未登录/过期会话：申请与取件均 401

- 级别 boundary / P2；执行方式：curl

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 不带任何凭据 POST `{BASE_URL}/api/media/{PAID_ORIG_KEY}/download` | — | HTTP 401 |
| 不带任何凭据 GET `{BASE_URL}/api/media-download/{TOKEN}` | — | HTTP 401 |
| 以过期会话凭据重复上两步 | — | 均 401（会话刷新机制为既有全局行为，此处只验证门禁生效） |

### MW-BD-09 限流：第 61 次/小时 POST /download 被拒

- 级别 boundary / P2；执行方式：curl 循环脚本；专用账号隔离执行（避免污染其它用例的限流配额）
- 前置条件：专用 paid 账号 + `orig` 素材；该账号本小时未消耗申请配额。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| `for i in $(seq 1 60); do curl …POST /download; done` | — | 前 60 次均 200 |
| 第 61 次同请求 | — | 非 200 限流拒绝（错误码记录实际值，见流程疑点 Q4），响应体无可用 `url` |
| 另一账号同时 POST `/download` | — | 200（限流按用户隔离，不误伤） |

### MW-BD-10 超长 WATERMARK_TEXT（>40 字符）不静默

- 级别 boundary / P2；执行方式：服务启动 + 浏览器/图片查看器观察（行为两可，见流程疑点 Q5）
- 前置条件：可配置 `WATERMARK_TEXT` 并重启的测试环境；flag=true；`{FREE}` 可生成。

| 操作 | 输入 | 预期可观察结果 |
| --- | --- | --- |
| 以 41+ 字符 `WATERMARK_TEXT` 启动 server 并生成一张图片 | — | 结果为以下二者之一并记录：a) 启动失败（校验拒绝，fail-fast）；b) 启动成功且生成图片上的水印文字不超过 40 字符（截断）。禁止出现「启动成功且水印原样超长」的静默态 |
| 还原默认配置重启 | — | 水印文案恢复默认 `infinite-canvas` |

## 边界覆盖

| 边界/等价类 | 覆盖状态 | 对应用例 | 说明 / 未覆盖原因 |
| --- | --- | --- | --- |
| 空输入（空 storageKey） | 已覆盖 | MW-BD-05 | 路由不匹配路径 |
| 无效格式（storageKey 非法/穿越串） | 已覆盖 | MW-BD-05 | 状态码口径见 Q6 |
| 无效格式（媒体格式：gif/非 mp4） | 已覆盖（go test） | MW-BD-02 | UI 不可稳定触发 gif 生成产物，不设 UI 用例（降级声明） |
| 无效格式（webp→PNG mime 变更） | 已覆盖（go test） | MW-BD-01 | |
| 超长输入（WATERMARK_TEXT>40） | 已覆盖 | MW-BD-10 | 截断/拒绝两可，见 Q5 |
| 特殊字符（token 篡改字节、编码穿越） | 已覆盖 | MW-SM-04、MW-BD-05 | |
| 重复提交（重复签发、token 重放） | 已覆盖 | MW-BD-06 | |
| 重复提交（生成回调双写/并发覆盖写） | 端到端未覆盖 | —（T5/T2 单测承接） | 原因：需并发注入钩子，UI/api 层不可稳定复现；幂等覆盖写与补偿删由单测锁死，评审确认单测存在 |
| 过期会话 | 已覆盖 | MW-BD-08 | |
| 数据不存在（不存在 key / 归属行已删） | 已覆盖 | MW-SM-05、MW-API-06 | 404 同口径防探测 |
| 网络失败（前端申请/取件失败） | 已覆盖 | MW-E2E-07 | |
| 网络失败（服务端 Presign 失败→500，不回退无签名 URL） | 端到端未覆盖 | —（建议 T4 单测承接） | 原因：需存储故障注入环境；异常矩阵 B 行为，评审确认 T4 单测是否覆盖，未覆盖则登记缺口 |
| S3 预签名 TTL | 已覆盖（S3 环境） | MW-API-04 | local 环境显式 skip 并注明，不允许静默假绿 |
| S3 负缓存进程重启自愈 | 单测覆盖；端到端未覆盖 | —（T2 负缓存三态单测） | 原因：端到端需手工向 S3 注入 orig 对象的权限，测试环境不具备 |
| 视频转码超时（WATERMARK_TIMEOUT=120s 截断） | 端到端未覆盖 | —（T5 fail-closed 单测承接） | 原因：需构造 >120s 转码样本，耗时不可控；超时→退款路径由单测注入覆盖 |
| EXIF 剥离 | 单测覆盖；端到端未覆盖 | —（T1 重编码单测） | 原因：端到端需 exiftool 类依赖，收益低 |
| 字体缺失启动 | 已覆盖 | MW-BD-03 | |
| 限流 61 次/小时 | 已覆盖 | MW-BD-09 | 错误码口径见 Q4 |
| 缓存头四态与 304 | 已覆盖（local） | MW-API-02 | S3 分支行为在该用例步骤 6 |
| HEAD/GET 同口径 | 已覆盖 | MW-API-03 | `X-Checksum` 口径见 Q8 |
| fail-closed（水印失败→退款+无干净字节） | 已覆盖（go test） | MW-API-08 | T5 落地前置 |
| orig 连带清理（删除/注销） | 已覆盖 | MW-API-06、MW-API-07 | local 文件系统核对；S3 驱动下以对象消失为准 |
| flag=false 回滚语义与闸门在线 | 已覆盖 | MW-E2E-05 | |
| 音频/稳定字节 immutable | 已覆盖 | MW-API-02 | |

## 流程疑点

以下为计划/契约未定义或两可的行为，编号提交评审裁定；用例侧不自行发明口径。

1. **社区作品「作者 vs 浏览者」申请下载的口径差异未被前端/文档表达**：严格归属口径下，浏览者对已发布作品 POST /download 必 404（MW-SM-05），但作者本人对自己已发布作品仍可签发（归属即权威）。建议口径：允许作者签发；社区页对浏览者不展示申请下载入口，或在文案中明确「仅作者可申请下载原件」。需产品确认并同步到 T7。
2. **flag 关闭期间生成、升级后无 orig 的历史素材「干净」语义混合**：免费用户在 flag=false 期间生成的素材本就无水印，升级 paid 后下载走 media url + `expiresAt=null`，「干净」来自从未打水印而非 orig 回流（与上线前历史干净件同属计划风险 S3 的接受范围）。建议口径：接受不回填；用户侧文案不得承诺「历史免费素材均为水印版」。
3. **token TTL 测试可调性**：TTL 钳制 ≤300s 且契约未提供请求参数，过期用例需实时等待约 5 分钟（MW-SM-04/MW-E2E-01）。建议口径：提供测试环境可调 TTL 变量（默认 300s，钳制 ≤300 不变），否则接受实时等待并将两条用例标注长耗时。
4. **限流超限的状态码/错误码未写明**：计划仅说「复用现有 limiter 模式，60 次/小时/用户」。建议口径：沿用现有 limiter 的 429 语义并在计划补记；用例侧 MW-BD-09 断言「非 200 + 限流错误码」，执行时记录实际值回填。
5. **超长 WATERMARK_TEXT（>40）行为两可**：计划写「超长截断或启动校验拒绝（以实现为准，不得静默）」。建议口径：启动校验拒绝（与字体缺失 fail-fast 同风格）；若实现为截断，需在启动日志留痕。MW-BD-10 按两种结果二选一记录。
6. **POST /download 对非法 storageKey 的状态码未在契约写明**：组内既有 GET/PUT/DELETE 均先过 `storageKeyRe` 返回 400。建议口径：与组内同口径 400；MW-BD-05 断言「非 200 且无 url 字段」以兼容两种实现。
7. **取件时 orig 已被删除（PUT 覆盖竞态）的响应语义未定义**：token 校验链只查媒体行存在，行存在而 orig 对象已被 PUT 覆盖连带删除时，GET /api/media-download/:token 返回 404 还是 500 契约未写。建议口径：404（与校验链失败同口径防探测）；MW-BD-07 断言「非 200 且不得返回被删 orig 旧字节」。
8. **Head 出 orig 时 `X-Checksum` 口径未定义**：现状 HEAD 返回媒体行 checksum；下发 orig 时该值若仍为媒体行 checksum，前端 `headMedia`（`web/src/services/api/media.ts`）拿到的 checksum 与实际 body 不一致。建议口径：出 orig 时 `X-Checksum` 反映 orig 内容或省略该头，T4 实现需明确并同步前端消费方。
9. **free/sunset 在申请下载链路的等价性与免费用户前端入口**：契约将 free/sunset 同归「返回 media url + null」一态，即免费用户走两步流程等价于直下水印版；前端是否对 free 用户保留下载按钮未单独定义。建议口径：保留按钮、不特判（两步流程对 free 自然退化）；T7 实现确认不对 free 返回 404 即可，sunset 不再单列用例（归入 free 等价类）。

## 自查记录

- UI 锚点全部使用页面 + i18n 文案键（`common.download`、`assets.downloadFailed`、`assets.downloadImage`、`assets.downloadVideo`），无散落裸 locator；项目无注册表，迁移前提已声明。
- 登录等长前置以「已登录」引用，不复制步骤；环境值与账号全部变量化；全文无任何凭据值。
- 会话顺序：注销（MW-API-07）、升级（MW-E2E-01/SM-06）、限流打满（MW-BD-09）均声明隔离或最后执行；无必须登出用例混入登录会话。
- 各步骤预期只断言该步自身可观察结果；T4 未落地用例标注「以契约为准」，T5 依赖用例标注「待实现/依赖落地」，S3/ffmpeg 缺失场景声明显式 skip，不允许静默假绿。
