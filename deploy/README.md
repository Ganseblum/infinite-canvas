# 环境与部署

代码只有一份，环境差异全部走环境变量：`deploy/env.test`（测试/预发布）与 `deploy/env.prod`（正式）只在本机和服务器上各留一份，**不提交进 git**，仓库里只保留 `.example` 模板。

## 三套环境

| 环境 | 前端地址 | 数据库 | 媒体 | 代码来源 |
| --- | --- | --- | --- | --- |
| 本地开发 | `http://localhost:3000`（vite dev） | 本机 MySQL `infinite_canvas` | 仓库下 `.local-media/` | 任意工作分支 |
| 测试/预发布 | `https://sim-xxx.example.com` → 主机 `3100` | `infinite_canvas_test` | 测试环境独立目录或桶 | 规划分支 / 合并后的 `main` |
| 正式 | `https://xxx.example.com` → 主机 `3200` | `infinite_canvas` | 正式环境独立目录或桶 | `main` 的版本 tag（见《Fork同步与部署约定》） |

## 用法

```bash
cp deploy/env.test.example deploy/env.test   # 填写测试环境变量
./deploy.sh test build                       # 构建测试镜像（infinite-canvas:test / infinite-canvas-api:test）
./deploy.sh test up -d

cp deploy/env.prod.example deploy/env.prod   # 填写正式环境变量
./deploy.sh prod build                       # 构建正式镜像（:prod）
./deploy.sh prod up -d
```

更新流程：`git pull` → `./deploy.sh <env> build` → `./deploy.sh <env> up -d`。
`deploy.sh` 内部就是 `docker compose -p infinite-canvas-test --env-file deploy/env.test …`。
不同项目名让容器、网络、命名卷（`mysqldata`、`media`）自动隔离，测试环境清库不会碰到正式数据；
镜像 tag 也按环境区分（env 文件里的 `APP_IMAGE` / `API_IMAGE`），测试构建不会覆盖正式正在用的镜像。
`up` 会固定 `--force-recreate app api`：app 容器内 nginx 启动时缓存了 api 容器 IP，api 重建后必须连带重建 app，否则 `/api` 反代仍指向旧 IP；db 不受影响。

app 对外端口由 env 文件的 `APP_PORT` 指定（测试 3100 / 正式 3200），只绑定 `127.0.0.1`，公网访问一律走宿主机反向代理。域名与证书由主机上的 nginx/Caddy 处理，把两个域名分别反代到 `127.0.0.1:3100` 与 `127.0.0.1:3200` 即可；前端与接口同源，容器内的 nginx 会把 `/api` 反代到 api 容器，因此不需要 CORS。宿主机反向代理要为 `/api/ai/` 关闭缓冲（`proxy_buffering off`）并放宽 `proxy_read_timeout`，`/api/media/` 放大 `client_max_body_size`；SSE 对话必须在反代后面验收，直连容器端口测不出问题。

测试环境还会叠加 `deploy/compose.test.yml`，给 db 额外映射 `127.0.0.1:13306`，供本地 Navicat 通过 SSH 隧道查看测试库。

## 数据库版本

数据库是 compose 自带的 MySQL 容器，版本 `mysql:8.4.11`。选 8.4 是因为它是 MySQL 当前 LTS（首选支持到 2029-04、延长支持到 2032-04）；8.0 的首选支持期已在 2026-04 结束，9.x 属于短周期 innovation 版本，均不用于新部署。补丁号钉死不跟浮动 tag，好处是本地、测试、正式三处跑的是同一个版本，行为可复现；升级时改 `docker-compose.yml` 里这一行并重建 db 容器。两个环境的库各自在命名卷里，版本升级互不影响。

## 每个环境必须不同的项

- `APP_IMAGE` / `API_IMAGE` 镜像 tag、`APP_PORT` 对外端口
- `DATABASE_URL`（库名或实例不同）、`MEDIA_ROOT` / S3 桶
- `JWT_SECRET`、`CREDENTIAL_MASTER_KEY`：**绝不能共用**，共用等于测试环境签发的令牌能在正式环境通过校验
- `ADMIN_EMAIL` / `ADMIN_PASSWORD`、`APP_BASE_URL`（邮件链接用它）、`SITE_ENV`
- `MAIL_DRIVER`（测试用 `log`，正式用 `smtp`）、`REGISTRATION_ENABLED`、`FREE_GRANT_*`

## 测试数据

测试账号与示例画布/素材写在代码里（`server/internal/db/seed_test_data.go`），随代码进 git，任何环境把 `SEED_TEST_DATA=true` 打开并在启动时执行一次即可导入，重复执行不会重复写入：

```bash
./deploy.sh test up -d          # 容器启动时按 env.test 里的 SEED_TEST_DATA 写入
```

本地不需要容器时，直接用同一开关跑 api：

```bash
SEED_TEST_DATA=true DATABASE_URL="canvas:li123456@tcp(127.0.0.1:3306)/infinite_canvas?charset=utf8mb4&parseTime=True&loc=UTC" \
  JWT_SECRET=local-verify-secret-at-least-32-characters-long \
  CREDENTIAL_MASTER_KEY=0123456789abcdef0123456789abcdef \
  ADMIN_EMAIL=admin@example.com ADMIN_PASSWORD=admin-local-12345 \
  COOKIE_SECURE=false MAIL_DRIVER=log \
  go run ./cmd/server
```

正式环境保持 `SEED_TEST_DATA=false`：测试账号密码是公开的，开启等于留了一个后门。

## 前端环境标识

`SITE_ENV` 会被容器入口脚本写进 `config.js`，只要不是 `production`，页面顶栏就会显示环境标识（如「测试环境」），避免在测试站上误当成正式站操作。`API_BASE_URL` 同理可在运行时注入，留空表示同源 `/api`。
