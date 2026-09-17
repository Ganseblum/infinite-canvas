#!/bin/sh
set -e

# nginx 官方镜像启动时会自动执行 /docker-entrypoint.d/*.sh，这里在启动前生成运行期 config.js。
#
# 刻意不复用 web/docker-entrypoint.sh：那个脚本会写 ANALYTICS_GA4_ID / ANALYTICS_BAIDU_ID，
# 运维只要在 admin 服务上多设一个统计变量，第三方统计脚本就会在管理后台控制台里跑起来，
# 把管理员的操作暴露给第三方。admin 只注入它自己要的键（API_BASE_URL / ADMIN_BASE_URL / SITE_ENV）。

CONFIG_PATH=/usr/share/nginx/html/config.js

# 只去掉可能破坏生成 JS 字符串字面量的字符（引号、反斜杠、换行），URL 与环境名的语义保持不变。
sanitize_text() {
    printf '%s' "$1" | tr -d '"\\' | tr -d '\r\n'
}

API_BASE_URL=$(sanitize_text "${API_BASE_URL:-}")
# 后台自己的对外地址，页面里指向主站的绝对链接用它拼（留空时页面降级为纯文本，不拼半截链接）。
ADMIN_BASE_URL=$(sanitize_text "${ADMIN_BASE_URL:-}")
SITE_ENV=$(sanitize_text "${SITE_ENV:-production}")

# fail-fast：空的 API_BASE_URL 对 admin 永远是错的。admin 域上没有 /api，
# 留空后所有请求会打到 admin 自己身上变成 404，页面看起来只是「登录没反应」这类静默故障。
# 宁可不启动并说清原因，也不要带着错配置跑起来。
if [ -z "$API_BASE_URL" ]; then
    echo "admin 启动失败：API_BASE_URL 为空。管理后台跨域直连主站 API，必须填非空的绝对地址（例如 https://sim-art.youc.online）；留空会让所有 /api 请求打到 admin 自己身上变成 404。" >&2
    exit 1
fi

cat > "$CONFIG_PATH" <<EOF
window.__RUNTIME_CONFIG__ = {
  API_BASE_URL: "${API_BASE_URL}",
  ADMIN_BASE_URL: "${ADMIN_BASE_URL}",
  SITE_ENV: "${SITE_ENV}"
};
EOF
