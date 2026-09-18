# 无限画布账号后端（server）

账号后端 Go 服务。第一期：注册、登录、令牌刷新、邮箱验证、找回密码、免费赠送资格与限流；第二期：画布、素材、生成记录的资源化接口，媒体对象存储（`local` / `s3` 双驱动）、单条画布乐观锁、软删除与 `ic_media` 只读媒体 cookie。默认监听 `:8080`，业务接口统一挂在 `/api` 下，`/healthz` 与 `/readyz` 仅供容器健康检查使用。

## 第二期接口一览

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET/POST | `/api/canvases` | 分页列表（只回摘要）/ 创建 |
| GET/PUT/PATCH/DELETE | `/api/canvases/{id}` | 详情 / `revision` 乐观锁全量更新 / 仅改标题 / 软删除 |
| GET/POST | `/api/assets` | 分页筛选（`kind`/`q`/`tag`），第一页带标签全集 `tags` / 创建 |
| GET/PATCH/DELETE | `/api/assets/{id}` | 详情 / 任意子集更新 / 软删除 |
| GET | `/api/generations` | 游标分页；`status=pending` 不分页一次返回（上限 100） |
| GET/DELETE | `/api/generations/{id}` | 详情 / 软删除；写入与终态收敛由第四期转发链路负责 |
| HEAD/GET/PUT/DELETE | `/api/media/{storageKey}` | 媒体读写；`GET/HEAD` 接受 Bearer 或 `ic_media` cookie，写只认 Bearer |

- 画布 `data` 后端字节上限 2 MB（`VALIDATION_FAILED`），`nodeCount`/`connectionCount`/`coverKey` 由服务端计算。
- 媒体 `storageKey` 必须匹配 `^(image|video|audio|file|video-reference|audio-reference):[A-Za-z0-9_-]{1,64}$`，落盘/对象路径由服务端按 `{用户 id}/{类型段}/{对象 id}` 重装。
- 上传前校验邮箱已验证与套餐 `max_file_bytes`；服务端自算 sha256，`X-Checksum` 仅供比对。
- `local` 驱动 `GET` 直接回流二进制（`ETag` + 一年 `immutable`）；`s3` 驱动 302 到对齐整点的预签名 URL（`Cache-Control: private, max-age={剩余秒数-60}`，绝不 `immutable`）。

## 本地启动

```bash
# 只起数据库。docker-compose.local.yml 把库映射到宿主机 127.0.0.1:13306
# （根目录 docker-compose.yml 是测试/正式部署用的，不映射数据库端口）
docker compose -f docker-compose.local.yml up -d db

cd server
cp ../.env.example .env   # 按需修改；本机 go run 时 DATABASE_URL 的 host 改为 127.0.0.1:13306
go run ./cmd/server
```

服务启动时会自动加载 `.env`（`server/internal/envload`）：依次尝试工作目录与上级目录，文件不存在时静默跳过，已导出的环境变量优先、不会被文件覆盖；容器部署没有 `.env`，行为不变。数据库 host 记住一句话：**容器内用 `db`，本机 `go run` 用 `127.0.0.1:13306`**。

本地没有 SMTP 时把 `MAIL_DRIVER` 置为 `log`，验证/重置链接会打到日志，可复制到浏览器完成流程。

## 测试

```bash
cd server
go test ./...
```

测试使用 SQLite 内存库，不依赖 MySQL 与外网。

## 构建镜像

```bash
cd server
docker build -t infinite-canvas-api:local .
```

镜像为多阶段构建，运行阶段仅含静态二进制与 CA 证书，非 root 用户运行。

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | `8080` | 监听端口，改动需同步 nginx 的 `proxy_pass` 与 compose 健康检查 |
| `DATABASE_URL` | 无 | MySQL 连接串，必填 |
| `JWT_SECRET` | 无 | access token 签名密钥，至少 32 字符 |
| `CREDENTIAL_MASTER_KEY` | 无 | 必须为 32 字节，第四期渠道密钥复用 |
| `APP_BASE_URL` | 无 | 站点外网地址，用于拼接邮件链接 |
| `COOKIE_SECURE` | `true` | refresh cookie 的 `Secure` 属性；无 TLS 环境置 `false` |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `TRUSTED_PROXIES` | 空 | 逗号分隔 CIDR；仅命中网段的来源才采用 `X-Real-IP` / `X-Forwarded-For`，默认不信任任何代理头 |
| `CORS_ALLOWED_ORIGINS` | 空 | 逗号分隔完整来源白名单（管理后台独立域名时填写）；留空即完全不启用 CORS |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | 无 | 首个管理员，仅在用户不存在时创建，不重置已有密码 |
| `SITE_ENV` | `production` | 前端环境标识：由 app 容器入口脚本写进 `config.js`，非 `production` 会在页面顶栏显示环境标识；Go 不读取该变量 |
| `SEED_TEST_DATA` | `false` | 启动时幂等写入测试账号与示例数据；只在测试/预发布环境开启，正式环境必须保持 false |
| `SEED_TEST_DATA_EMAIL` / `SEED_TEST_DATA_PASSWORD` | `test@example.com` / `test123456` | 测试账号邮箱与密码 |
| `ENTITLEMENT_DAYS` | `30` | 充值默认延长的付费权益天数，档位上的 `entitlement_days` 可覆盖 |
| `MAIL_DRIVER` | `smtp` | `smtp` 走真实邮件服务，`log` 只打日志 |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USERNAME` / `SMTP_PASSWORD` / `SMTP_FROM` | 无 | `MAIL_DRIVER=smtp` 时必填，QQ 邮箱用授权码 |
| `SMTP_SECURITY` | `ssl` | `ssl` 或 `starttls` |
| `REGISTRATION_ENABLED` | `true` | 注册总开关 |
| `FREE_GRANT_ENABLED` | `true` | 免费赠送总开关 |
| `FREE_GRANT_CAMPAIGN_ID` | 无 | 活动标识，按 `(user_id, campaign_id)` 幂等；启用赠送时必填 |
| `FREE_GRANT_DAILY_BUDGET_MICROS` | 无 | 每日赠送预算上限；启用赠送时必填且大于 0 |
| `FREE_GRANT_RISK_THRESHOLD` | 无 | 风险评分阈值；启用赠送时必填且大于 0 |
| `STORAGE_DRIVER` | `local` | 媒体存储驱动：`local` 或 `s3` |
| `MEDIA_ROOT` | `/data/media` | `local` 驱动的落盘根目录（compose 对应 `media` 卷） |
| `S3_ENDPOINT` | 无 | S3 兼容服务地址，`STORAGE_DRIVER=s3` 时必填，需带协议与主机 |
| `S3_REGION` | 无 | 区域标识，R2 填 `auto`；`s3` 时必填 |
| `S3_BUCKET` | 无 | 桶名，必须保持私有；`s3` 时必填 |
| `S3_ACCESS_KEY_ID` / `S3_SECRET_ACCESS_KEY` | 无 | 只给单个桶读写的访问密钥；`s3` 时必填 |
| `S3_FORCE_PATH_STYLE` | `false` | 路径风格寻址，MinIO 与部分自建服务设 `true` |
| `S3_PRESIGN_ALIGN` | `1h` | 预签名签发时间的对齐粒度 |
| `S3_PRESIGN_TTL` | `2h` | 预签名有效期，必须等于 2 × `S3_PRESIGN_ALIGN`，否则启动失败 |
| `AI_IMAGE_TIMEOUT` / `AI_AUDIO_TIMEOUT` / `AI_STREAM_TIMEOUT` / `AI_STREAM_IDLE_TIMEOUT` / `AI_VIDEO_TASK_TIMEOUT` | `180s` / `120s` / `600s` / `60s` / `20m` | 各能力转发上游的超时 |
| `AI_ALLOW_PRIVATE_UPSTREAM` | `false` | 允许上游指向内网地址；仅本地调试开启，正式环境必须为 false |
| `MODERATION_ENABLED` | `false` | 内容审核总开关；开启时 `MODERATION_FAIL_MODE` 必填，缺失启动失败 |
| `MODERATION_PROVIDER` | `fake` | `fake`（本地联调）或 `nsfwjs+detoxify`（真实推理 sidecar） |
| `MODERATION_NSFWJS_ENDPOINT` / `MODERATION_DETOXIFY_ENDPOINT` | 无 | 两个推理 sidecar 的地址；`nsfwjs+detoxify` 时必填，compose 内填服务名地址 |
| `MODERATION_FAKE_REJECT_TEXTS` | 无 | 仅 `fake` 生效：命中即触发拒绝的文本片段，逗号分隔，用于拒绝链路联调 |
| `MODERATION_IMAGE_THRESHOLD` / `MODERATION_TEXT_THRESHOLD` | `0.6` / `0.8` | 违规判定阈值，取值 (0,1] |
| `MODERATION_VIDEO_SAMPLE_FPS` | `1` | 视频抽帧频率 |
| `MODERATION_FAIL_MODE` | 无 | `reject` 或 `allow`，无默认值；审核开启时必填，上游故障按它拒绝或放行 |
| `MODERATION_TIMEOUT` | `5s` | 单次审核请求超时 |
| `MODERATION_POLICY_VERSION` | `v1` | 审核策略版本号，写入审核记录 |
| `MODERATION_QUARANTINE_TTL` | `24h` | 隔离区保留时长，过期物理删除原件 |
| `MODERATION_CACHE_TTL` | `10m` | 审核结论缓存时长 |

## 媒体存储说明

- **写路径经后端代理**：`PUT /api/media/{storageKey}` 流式转发到存储驱动，写前校验邮箱验证状态与 `plans.max_file_bytes`；v1 不做预签名直传。
- **桶必须私有**：R2 的 `r2.dev` 公开域名一旦启用，预签名与鉴权设计全部失效，上线前需实测匿名访问被拒。
- **`ic_media` cookie**：登录与刷新时随 refresh token 一起轮换下发，`HttpOnly; Secure(由 COOKIE_SECURE 控制); SameSite=Lax; Path=/api/media`，有效期 30 天；登出会同时清除 `ic_refresh` 与 `ic_media`。它只能读当前用户自己的媒体，不能调业务接口，`PUT`/`DELETE` 不认它。
- **S3 客户端**：AWS SDK for Go v2，写入/读取/删除走 SDK；预签名对齐时间显式传给 SDK 的 SigV4 签名器，签名逻辑不手写。

## 健康检查

- `GET /healthz`：进程存活，不做 I/O。
- `GET /readyz`：对数据库执行 Ping，通过返回 200，失败返回 503。
