# 环境与部署

代码只有一份，环境差异全部走环境变量：`deploy/env.test`（测试/预发布）与 `deploy/env.prod`（正式）只在本机和服务器上各留一份，**不提交进 git**，仓库里只保留 `.example` 模板。

## 三套环境

| 环境 | 前端地址 | 数据库 | 媒体 | 代码来源 |
| --- | --- | --- | --- | --- |
| 本地开发 | `http://localhost:3000`（vite dev） | 本机 MySQL `infinite_canvas` | 仓库下 `.local-media/` | 任意工作分支 |
| 测试/预发布 | `https://sim-xxx.example.com` → 主机 `3100` | `infinite_canvas_test` | 测试环境独立目录或桶 | 规划分支 / 合并后的 `main` |
| 正式 | `https://xxx.example.com` → 主机 `3000` | `infinite_canvas` | 正式环境独立目录或桶 | `main` 的版本 tag（见《Fork同步与部署约定》） |

## 用法

```bash
cp deploy/env.test.example deploy/env.test   # 填写测试环境变量
./deploy.sh test up -d

cp deploy/env.prod.example deploy/env.prod   # 填写正式环境变量
./deploy.sh prod up -d
```

`deploy.sh` 内部就是 `docker compose -p infinite-canvas-test --env-file deploy/env.test …`。
不同项目名让容器、网络、命名卷（`mysqldata`、`media`）自动隔离，测试环境清库不会碰到正式数据。

域名与证书由主机上的反向代理（nginx/Caddy 或云厂商负载均衡）处理，把两个域名分别指向 `127.0.0.1:3100` 与 `127.0.0.1:3000` 即可；前端与接口同源，容器内的 nginx 会把 `/api` 反代到 api 容器，因此不需要 CORS。

## 每个环境必须不同的项

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
