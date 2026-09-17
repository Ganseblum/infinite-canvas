# 环境与部署

代码只有一份，环境差异全部走环境变量：`deploy/env.test`（测试/预发布）与 `deploy/env.prod`（正式）只在本机和服务器上各留一份，**不提交进 git**，仓库里只保留 `.example` 模板。

## 两套环境，本地开发共用测试数据

服务器只部署测试、正式两套环境，不再单独建立开发数据库。本地电脑可以运行前端或前后端，通过 SSH 连接测试环境；本地与服务器测试站属于同一个逻辑测试环境。

| 环境 | 前端地址 | 数据库 | 媒体 | 代码来源 |
| --- | --- | --- | --- | --- |
| 本地开发 | `http://localhost:3000`（Vite） | 共用服务器 `infinite_canvas_test` | 前端联调使用服务器测试媒体；双 Go 模式需共用测试桶 | 与测试库结构兼容的工作分支 |
| 测试/预发布 | `https://sim-xxx.example.com` → 主机 `3100` | `infinite_canvas_test` | 测试环境独立目录或桶 | 规划分支 / 合并后的 `main` |
| 正式 | `https://xxx.example.com` → 主机 `3200` | `infinite_canvas` | 正式环境独立目录或桶 | `main` 的版本 tag（见《Fork同步与部署约定》） |

本地修改测试库里的数据会直接影响测试站；代码发布不会把测试账号、订单、画布或媒体自动复制进正式环境。当前 compose 为测试、正式各启动一个 MySQL 实例，两者的数据卷独立。

| 实施阶段 | 运行方式 | 当前状态 |
| --- | --- | --- |
| 先部署测试站 | 服务器 app / api / db，本地用 DBeaver 查看测试库 | 编排已具备，服务器部署及验收待实施 |
| 本地前端联调 | 本地 Vite → SSH → 服务器测试 API | 现有代理可以连接隧道，Cookie / 媒体 / SSE 待验收 |
| 本地前后端共用测试数据 | 本地 Go → SSH → 测试库，两端共用媒体 | 待补共享媒体及启动执行开关，不能只改数据库地址就并行运行 |
| 正式上线 | 独立正式 app / api / db，固定已验收版本 | 测试通过、备份恢复和上线准备完成后实施 |

## 服务器部署顺序

服务器资源、真实域名、已有服务和具体目录安排记录在《我的规划/部署方案（测试与正式环境）.md》。手册不保存密码或密钥；环境文件仍只保存在本机和服务器，不提交 git。

1. 复核现有容器、宿主机 Nginx、端口和证书。测试、正式使用独立代码目录，避免更新测试代码时改变正式构建目录。
2. 在测试目录检出明确的功能分支或提交，填写 `deploy/env.test`：数据库凭据、真实 `APP_BASE_URL`、随机密钥、管理员和测试数据开关。数据库连接串使用容器服务名 `db`。
3. 建数据目录并授权：`mkdir -p data/mysql data/media && chown -R 10001:10001 data/media`。api 容器以 uid 10001 运行；绑挂载不像命名卷那样继承镜像内目录的属主，漏掉这步媒体写入会失败。

> **不要在检出目录上执行递归 chown。** `data/` 直接位于检出目录内，`chown -R root:root .`（同步代码、修属主时很容易顺手敲）会把 `data/media` 的 `10001` 和 `data/mysql` 的 `999` 一起改回 root，容器随即失去写权限。**症状是生成耗时几十秒后返回 502 `UPSTREAM_ERROR`**——看起来像上游故障，实际是产物落盘失败（`open /data/media/.../image/.upload-*: permission denied`），同时 MySQL 虽仍能读，但新建表、日志轮转与重启都会出问题。修复：`chown -R 10001:10001 data/media && chown -R 999:999 data/mysql && docker restart infinite-canvas-test-db-1`。
4. 构建并启动测试 app / api / db。
5. 宿主机 Nginx 新增测试域名：以 `deploy/nginx-sim-art.youc.online.conf` 为模板，先只上 HTTP（此时证书还不存在，直接写 TLS 段会让 `nginx -t` 失败），`nginx -t` 通过后 `systemctl reload nginx`，再 `certbot --nginx --redirect -d sim-art.youc.online` 签发证书并补上跳转，反代到 `127.0.0.1:3100`。
6. 验收登录、邮件、画布保存、媒体、AI、SSE、容器重启和数据库隧道；再补齐双 Go 共用测试数据的前置条件。
7. 测试完成后，经用户明确同意合入 `main`，从已验收提交创建版本 tag。正式不直接部署规划分支或上游镜像。
8. 在正式目录检出版本 tag，填写独立 `deploy/env.prod`，完成备份恢复准备，再启动正式服务到 `127.0.0.1:3200`。
9. 先验收正式容器和反代，再切正式域名；保留既有 AI 上游服务的独立域名。

随机串可分别用 `openssl rand -hex 32` 生成 JWT secret、`openssl rand -hex 16` 生成凭据主密钥。后者输出 32 个 ASCII 字符，满足当前代码的 32 字节要求；每套环境分别生成，不使用模板占位值。

首次正式部署也要确认 SMTP、内容审核、实际支付渠道和管理员配置。暂不上架的支付渠道保持未配置；微信私钥必须以只读文件挂进 API 容器，变量填写容器内 PEM 路径，当前 compose 尚未包含该挂载。STARTTLS 邮件发送分支仍需修复，默认 SSL 路径须实测验证与重置邮件。

构建前需补齐 `.dockerignore` 对实际 `deploy/env.test`、`deploy/env.prod` 和本地实施手册的排除；`.gitignore` 不会自动排除 Docker 构建上下文。

## 构建与启动

```bash
cp deploy/env.test.example deploy/env.test   # 填写测试环境变量
mkdir -p data/mysql data/media               # 仅首次部署需要
chown -R 10001:10001 data/media              # api 容器 uid；漏掉会导致媒体写入失败
./deploy.sh test build                       # 构建测试镜像（infinite-canvas:test / infinite-canvas-api:test）
./deploy.sh test up -d

cp deploy/env.prod.example deploy/env.prod   # 填写正式变量，将镜像 tag 改为已验收版本号
./deploy.sh prod build                       # 构建该版本的正式镜像
./deploy.sh prod up -d
```

测试更新流程：记录目标提交 → 在测试目录更新代码 → `./deploy.sh test build` → `./deploy.sh test up -d`。正式目录检出已验收版本 tag；将 `APP_IMAGE` / `API_IMAGE` 设置为包含版本号的独立标签并记录镜像 ID，不反复覆盖唯一的 `:prod` 镜像作为回滚依据。现有脚本支持 env 提供的镜像标签，不会自动检查分支、版本或保留旧镜像。
`deploy.sh` 内部就是 `docker compose -p infinite-canvas-test --env-file deploy/env.test …`。
不同项目名让容器、网络与数据目录自动隔离（数据库与媒体落在各自仓库目录的 `./data` 下），测试环境清库不会碰到正式数据；
镜像 tag 也按环境区分（env 文件里的 `APP_IMAGE` / `API_IMAGE`），测试构建不会覆盖正式正在用的镜像。
`up` 会固定 `--force-recreate app api`：app 容器内 nginx 启动时缓存了 api 容器 IP，api 重建后必须连带重建 app，否则 `/api` 反代仍指向旧 IP；db 不受影响。

app 对外端口由 env 文件的 `APP_PORT` 指定（测试 3100 / 正式 3200），只绑定 `127.0.0.1`，公网访问一律走宿主机反向代理。域名与证书由主机上的 nginx/Caddy 处理，把两个域名分别反代到 `127.0.0.1:3100` 与 `127.0.0.1:3200` 即可；前端与接口同源，容器内的 nginx 会把 `/api` 反代到 api 容器，因此不需要 CORS。宿主机反向代理要为 `/api/ai/` 关闭缓冲（`proxy_buffering off`）并放宽 `proxy_read_timeout`，`/api/media/` 放大 `client_max_body_size`；SSE 对话必须在反代后面验收，直连容器端口测不出问题。

测试环境还会叠加 `deploy/compose.test.yml`，给 db 额外映射 `127.0.0.1:13306`，供本地 Go、DBeaver 或 Navicat 通过 SSH 隧道连接测试库。正式数据库不映射宿主机端口，不开放公网 MySQL。

## 本地开发与数据库工具

### 数据库隧道：本地 Go 与 DBeaver 共用

在电脑打开终端，替换服务器地址后保持会话运行：

```bash
ssh -N -L 127.0.0.1:13306:127.0.0.1:13306 root@服务器地址
```

本地 Go 的连接串改为 `canvas_test:测试库密码@tcp(127.0.0.1:13306)/infinite_canvas_test?charset=utf8mb4&parseTime=True&loc=UTC`。服务器 API 仍使用 `db:3306`，两者指向同一个测试库。完整双 Go 模式的前置条件见下文，当前不要直接启动两个完整后端。

DBeaver 在电脑上安装，新建 MySQL 连接：

| 配置 | 值 |
| --- | --- |
| 主机 / 端口 | `127.0.0.1` / `13306` |
| 数据库 | `infinite_canvas_test` |
| 用户名 / 密码 | 测试环境 `MYSQL_USER` / `MYSQL_PASSWORD`，不是 SSH root 凭据 |
| SSH 选项 | 已使用终端隧道时关闭，避免再建立一层 |

也可只在 DBeaver 内开启 SSH：SSH 主机填服务器地址、端口 `22`、用户填获授权的 SSH 用户；MySQL 主机仍填 `127.0.0.1`、端口 `13306`。其默认隧道端口由 DBeaver 管理，本地 Go 不应依赖这个动态端口，仍使用固定的终端隧道。[DBeaver SSH 文档](https://dbeaver.com/docs/dbeaver/SSH-Configuration/)

Community 版用于表格数据查看、筛选、SQL 与 ER 表结构关系图；查询结果生成柱状图、折线图或饼图属于 Lite / Enterprise / Ultimate 功能。DBeaver 不承担媒体文件存储。[图表功能说明](https://dbeaver.com/docs/dbeaver/Managing-Charts/)

### 只改前端：使用服务器测试 API

不启动本地 Go，保持本地 API base 为空，将现有 Vite 代理的本机 `8080` 接到服务器测试 app：

```bash
ssh -N -L 127.0.0.1:8080:127.0.0.1:3100 root@服务器地址
```

另开终端启动前端：

```bash
cd web
bun run dev
```

打开 `http://localhost:3000`，浏览器 `/api` → Vite → 隧道 → 服务器 app → 测试 API。前端保存即热更新，不需要发布测试镜像，也不依赖测试域名；媒体仍由服务器测试 API 读取。

此模式本机 `8080` 必须空闲，与本地 Go 模式互斥。浏览器仍需验收刷新登录和媒体 Cookie；localhost 与普通局域网 HTTP 地址行为不同，有差异时使用本地 HTTPS，服务器保持 `COOKIE_SECURE=true`。不要只填写跨域 `VITE_API_BASE_URL`：当前 Go 后端没有 CORS 配置。

### 改前后端：双 Go 共用测试库的目标方案（待实施）

- **同一套媒体存储**：两端使用同一个私有测试 S3 兼容桶；正式使用独立桶或媒体卷。当前 `local` 驱动读取各自 `MEDIA_ROOT`，电脑目录不能直接共享服务器命名卷。提供方、端点与桶在实施时确定；预签名地址须能被两端浏览器访问，并配置桶 CORS，验收上传、生成、读取、下载、画布导出和删除。
- **单一启动维护执行者**：默认服务器测试 Go 执行 AutoMigrate、档位/模型 seed、管理员初始化、测试 seed 和启动滞留请求收敛，本地 Go 关闭这些动作。修改表结构时先停止本地 Go，由指定执行者应用变更，两端代码均需兼容测试库结构。
- **单一后台任务执行者**：默认服务器测试 Go 负责订单扫描、视频轮询、隔离区清理和注销到期处理，本地 Go 关闭这些任务。当前代码没有执行开关，需补齐后才能并行；不能仅关闭定时任务而保留启动扫描。
- **共享测试凭据**：本地与服务器测试 Go 使用相同测试 `JWT_SECRET`、`CREDENTIAL_MASTER_KEY` 和测试渠道；凭据主密钥不一致会导致渠道与隔离原件无法解密。正式密钥独立。登录 Cookie 仍按 localhost / 测试域名各自保存，不自动共享登录状态。
- **入口配置分别设置**：本地 `APP_BASE_URL` 指向本地页面，本地 HTTP Go 使用 `COOKIE_SECURE=false`，服务器 HTTPS 测试 Go 保持 `true`；邮件、真实支付回调在服务器测试站验收。本地前端保留同源 `/api`，Vite 代理到本地 Go `8080`。
- **设置一致性**：当前站点设置按进程缓存，DBeaver 或另一端修改设置后不会自动刷新所有进程；需明确重启或刷新方式。限流和部分并发状态也按进程管理，该模式用于开发联调，不作为正式多实例运行方案。

执行开关与共享媒体部署仍在 TODO，以上不是现成可用的新增环境变量。前置条件完成后，在本地仓库根目录 `.env.local` 准备本地 Go 的开发配置，不入库；使用可被 shell 加载的赋值语法，密码和连接串正确引用。当前 Go 只读进程环境，不会自动加载 `.env`，启动前明确加载/export，再运行 `go run ./cmd/server`。

## 数据库版本

数据库是 compose 自带的 MySQL 容器，固定版本 `mysql:8.4.11`（8.4 LTS），测试和正式各有独立数据卷；本地开发直接连接测试库，不启动第三个数据库。升级数据库版本前先备份并在测试环境验证，正式升级使用相同已验收版本；不要通过删除数据卷来更新数据库账号密码。

## 测试与正式必须不同的项

- `APP_IMAGE` / `API_IMAGE` 镜像 tag、`APP_PORT` 对外端口
- `DATABASE_URL`（库名或实例不同）、`MEDIA_ROOT` / S3 桶
- `JWT_SECRET`、`CREDENTIAL_MASTER_KEY`：测试与正式不共用；JWT 密钥相同会让测试签发的令牌在正式通过校验，凭据主密钥相同会失去渠道密钥的环境隔离。本地与服务器测试属于同一逻辑环境，按上文共享测试密钥。
- `ADMIN_EMAIL` / `ADMIN_PASSWORD`、`APP_BASE_URL`（邮件链接用它）、`SITE_ENV`
- `MAIL_DRIVER`（测试用 `log`，正式用 `smtp`）、`REGISTRATION_ENABLED`、`FREE_GRANT_*`

## 测试数据

测试账号与示例画布/素材写在代码里（`server/internal/db/seed_test_data.go`），随代码进 git，任何环境把 `SEED_TEST_DATA=true` 打开并在启动时执行一次即可导入，重复执行不会重复写入：

```bash
./deploy.sh test up -d          # 容器启动时按 env.test 里的 SEED_TEST_DATA 写入
```

共享测试库时，由指定的服务器测试 Go 初始化测试数据，本地 Go 不重复执行 seed。测试账号和样例数据仍由代码维护，不需要从正式库复制真实数据。

正式环境保持 `SEED_TEST_DATA=false`：测试账号密码是公开的，开启等于留了一个后门。

## 前端环境标识

`SITE_ENV` 会被容器入口脚本写进 `config.js`，只要不是 `production`，页面顶栏就会显示环境标识（如「测试环境」），避免在测试站上误当成正式站操作。`API_BASE_URL` 同理可在运行时注入，留空表示同源 `/api`。

## 管理后台

管理后台不是独立应用：页面是同一个前端里的 `/admin/*` 路由，接口在同一个 api 服务（`/api/admin/*`），因此**不需要额外部署服务、端口、域名或镜像**。前端路由守卫只负责跳转，权限由 api 的服务端中间件强制（`Auth` + `AdminOnly`）。

管理员账号由 api 启动时按 `ADMIN_EMAIL` / `ADMIN_PASSWORD` 初始化：账号不存在时创建，已存在但角色不是 admin 时提升为 admin；**已存在时不会用 `ADMIN_PASSWORD` 重设密码**，所以忘记管理员密码时改 env 文件无效，只能改库或走密码重置流程。这两个变量缺失会导致 api 启动失败，测试与正式环境各自使用独立的邮箱与密码。

安全相关的两点：

- 管理后台与其他页面同域同源，公网可访问；如需收敛，反向代理访问策略同时覆盖 `/admin` 和 `/api/admin/`，仅隐藏页面入口不能限制管理接口（尚未实施，上线前决定）。
- 管理后台配置的 AI 渠道密钥用 `CREDENTIAL_MASTER_KEY` 加密存库，该密钥必须与正式环境隔离，本地与服务器测试共享测试密钥，并且必须随备份一起保存；密钥丢失则渠道密钥无法解密。

## 内容审核（可选）

`MODERATION_ENABLED=false` 时无需任何额外组件。切到 `MODERATION_PROVIDER=nsfwjs+detoxify` 前，需要额外准备两样东西（当前 compose 与 api 镜像尚未包含）：

- 两个推理 sidecar（NSFWJS 的 Node 服务与 Detoxify 的 Python 服务）加入 compose 网络，并把地址填进 `MODERATION_NSFWJS_ENDPOINT` / `MODERATION_DETOXIFY_ENDPOINT`。
- 视频审核需要 api 容器内有 `ffmpeg` 用于抽帧；缺失时按 `MODERATION_FAIL_MODE` 拒绝或放行，不会静默通过。

组件补齐前可以用 `fake` 做拒绝、放行和错误联调，不代表真实内容检测有效；当前测试模板默认关闭审核。公开运营前补齐真实审核，明确 `MODERATION_FAIL_MODE` 并验收视频抽帧覆盖。

## 验收与备份恢复

- 测试入口：HTTPS 证书匹配，页面显示测试标识，刷新业务路由可打开，运行期 `config.js` 正确。
- 业务链路：登录与刷新 Cookie、验证/重置邮件、画布保存与冲突、素材上传和读取、AI 渠道及生成、SSE 连续输出、重启后视频任务恢复。
- 数据工具：DBeaver 经隧道能看到同一个测试库；只在开发模式前置条件完成后验收本地 Go 写入、测试站读取及双向媒体操作。余额、订单、流水通过业务接口调整，避免直接编辑单表破坏账本一致性。
- 数据隔离：正式库、媒体和密钥独立，正式关闭测试 seed，测试操作不会改变正式数据。

正式部署前建立数据库、媒体、env/密钥和版本/镜像记录的联合备份，在测试环境演练恢复。数据库可用 `mysqldump --single-transaction` 做一致性导出；联合备份时协调媒体写入/删除和其他写入者，恢复所用数据库与媒体必须对应同一恢复点。共享测试库做恢复演练时先停止本地 Go 和其他测试写入者。

每次正式升级前备份并保留旧版本镜像。当前 AutoMigrate 没有结构回滚，不能只退回旧镜像就宣称数据回滚成功；涉及不兼容结构时，在暂停写入后恢复对应数据库、媒体和密钥。公开上线后的结构变更需落实版本化 migration。定时备份频率、保留策略和异地位置在实施时确定，当前未配置自动备份。

普通媒体的保留期/孤儿清理目前只有管理员手动入口，尚无周期执行任务。长期运行需要补齐清理调度和磁盘监控，不把已有隔离区定时清理当成所有媒体的自动回收。

## 本地连接测试环境（SSH 隧道）

本地开发不单独建库，测试环境的 MySQL 与 API 都只绑在服务器的 `127.0.0.1` 上，本地通过 SSH 隧道访问。**隧道是本机与服务器之间的一条通道，断了本地就连不上——报错很像服务端故障，先查隧道。**

### 两条隧道

| 本地端口 | 转发到服务器 | 用途 |
| --- | --- | --- |
| `13306` | `127.0.0.1:13306`（db 容器的端口映射） | Navicat / DBeaver / mysql 命令行直连测试库 |
| `8080` | `127.0.0.1:3100`（app 容器） | 本地 Vite 的 `/api` 代理目标，见 `web/vite.config.ts` 的 `server.proxy` |

`8080` 这条之所以能直接顶替本地 Go：Vite 把 `/api` 写死代理到 `127.0.0.1:8080`，隧道把该端口接到测试站的 app 容器，因此**不需要改前端配置**，本地页面就与测试站共用同一份数据和同一个后端。代价是本地的后端代码改动不会生效——要验证后端改动必须重新部署测试站。

### 启动

```bash
ssh -f -N -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
    -L 13306:127.0.0.1:13306 root@<服务器>      # 数据库
ssh -f -N -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
    -L 8080:127.0.0.1:3100 root@<服务器>        # API
```

`-f` 转后台、`-N` 不执行远程命令。`ServerAliveInterval` 让连接空闲断开时进程自己退出，而不是变成假死。

关闭：`pkill -f "13306:127.0.0.1:13306"`、`pkill -f "8080:127.0.0.1:3100"`。

### 连接凭据

数据库名 `infinite_canvas_test`，用户名与密码在服务器的 `deploy/env.test`（`MYSQL_USER` / `MYSQL_PASSWORD` / `MYSQL_ROOT_PASSWORD`）。Navicat 等客户端**只填常规页签**（主机 `127.0.0.1`、端口 `13306`），不要同时配客户端自带的 SSH 通道——两套通道叠加会互相干扰。

### 故障排查：隧道假死

**症状**：客户端报 `2013 - Lost connection to server at 'handshake: reading initial communication packet'`（Navicat 常见写法），或命令行报 `ERROR 2013 ... system error: 2` / `ERROR 2003 ... (61)`。

**判据**：下面两条同时成立就是假死——

1. `lsof -nP -iTCP:13306 | grep LISTEN` **仍有输出**（端口在监听，所以看起来"隧道是好的"）
2. 但 `mysql -h 127.0.0.1 -P 13306 -u<用户> -p<密码> -e "SELECT 1"` 也失败

**成因**：本地 SSH 进程还活着，但它与服务器之间的底层连接已经断开，端口继续接受连接却转发不出去。端口上通常能看到残留的 `CLOSE_WAIT` 连接。

**处理**：直接重启隧道（见上），不要怀疑数据库或容器。**先用同一台机器上的 `mysql` 命令行复现一次**——如果命令行也失败，问题在隧道；只有 Navicat 失败而命令行正常，才是客户端配置问题。

服务器侧的判据：`ss -tlnp | grep 13306` 应看到 `docker-proxy` 在监听，`docker ps` 里三个容器应都是 Up。这两项正常就说明问题在本机隧道。

### 注意

- 隧道直达**测试环境的库**，Navicat 里的改动会立刻影响测试站和连同一后端的本地页面。
- 本机若曾跑过独立的本地 Go + 本地 MySQL，那是另一套数据，与隧道无关；确认连接指向哪个端口，别把两套数据搞混。
- 本机不要同时再起一个连测试库的 Go 进程：迁移、定时任务会双跑，且两端 `MEDIA_ROOT` 不同导致媒体互相读不到。共享媒体与执行开关尚未实现。
