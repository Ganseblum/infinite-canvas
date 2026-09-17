# 管理后台（admin/）静态产物构建 + nginx 运行镜像。
#
# 构建上下文必须是仓库根（compose 里写 context: .，不要改成 context: ./admin）：
# admin 通过 vite 别名 @ -> web/src 复用主站外壳（清单见 admin/scripts/web-whitelist.mjs），
# 只把 admin/ 当上下文，别名目标与 web/src/styles/globals.css 都不在上下文里，构建必挂在解析阶段。
#
# 依赖要装两处，缺一不可：
#   - web/node_modules：admin/src/styles/globals.css 直接 @import web/src/styles/globals.css，
#     那份 CSS 用裸包名引 tailwindcss / tw-animate-css / shadcn，按 CSS 文件所在目录（web/src/styles）
#     向上找 node_modules，admin/node_modules 不在解析路径上；白名单里的 web 模块（zustand / antd /
#     i18next 等）同理只在 web/node_modules 命中。这也与本地开发一致（本地两个 node_modules 并存）。
#   - admin/node_modules：admin 自己的工具链与依赖，版本与 web 独立（如 tailwindcss 4.3.x / 4.2.x）。
FROM oven/bun:1.3.13 AS admin-build

WORKDIR /app/web
COPY web/package.json web/bun.lock ./
RUN --mount=type=cache,target=/root/.bun/install/cache bun install --cache-dir=/root/.bun/install/cache

WORKDIR /app/admin
COPY admin/package.json admin/bun.lock ./
RUN --mount=type=cache,target=/root/.bun/install/cache bun install --cache-dir=/root/.bun/install/cache

# web/src 是 @ 别名的目标，必须进镜像：admin/src/styles/globals.css 的 @import 与白名单模块都指向它。
COPY web/src /app/web/src

# 逐个复制 admin 的源码，刻意不写 COPY admin ./：.dockerignore 没有排除 admin/node_modules，
# 整目录复制会把宿主机的 node_modules 合并进上面装好的容器依赖（平台不符、原生二进制错），
# 表现为构建随机失败或产物异常。admin/ 下新增顶层文件时，要在这一组 COPY 里补上。
COPY admin/index.html admin/postcss.config.mjs admin/tsconfig.json admin/vite.config.ts ./
COPY admin/src ./src
COPY admin/scripts ./scripts
COPY admin/public ./public

# prebuild 会跑 scripts/check-import-boundary.mjs 断言「admin/src 里的 @/ 只引用白名单」，
# 所以 scripts/ 必须在上下文里，且构建失败要当成真实失败处理（它挡的是别名越界）。
# vite.config.ts 不读根 VERSION / CHANGELOG.md（只有 web/vite.config.ts 读），这里不需要复制这两个文件。
RUN bun run build

# 运行镜像：纯静态，容器内不做任何 /api 反代——admin 跨域直连主站 API，这正是独立容器的目的。
FROM nginx:1.27-alpine

COPY --from=admin-build /app/admin/dist /usr/share/nginx/html
COPY admin/nginx.conf /etc/nginx/conf.d/default.conf
# 入口脚本只注入 admin 自己要的键；不要复用 web/docker-entrypoint.sh：
# 那个脚本会写 ANALYTICS_* 键，运维在 admin 服务上多设一个统计变量就会让第三方统计脚本跑在管理后台里。
COPY admin/docker-entrypoint.sh /docker-entrypoint.d/40-admin-runtime-config.sh
RUN chmod +x /docker-entrypoint.d/40-admin-runtime-config.sh

EXPOSE 3000
