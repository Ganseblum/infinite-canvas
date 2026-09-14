#!/usr/bin/env bash
# 按环境运行 docker compose：测试环境与正式环境用不同项目名，
# 容器名、网络、数据卷（数据库、媒体）三样都会自动隔离，不会互相覆盖。
#
#   ./deploy.sh test up -d            启动测试环境（sim-xxx 域名指向主机 3100 端口）
#   ./deploy.sh prod up -d            启动正式环境（xxx 域名指向主机 3000 端口）
#   ./deploy.sh test logs -f api      看测试环境 api 日志
#   ./deploy.sh prod ps               看正式环境容器状态
#   ./deploy.sh test down             停止测试环境（数据卷保留）
#
# 依赖：docker compose（v2）。环境变量文件放在 deploy/env.test、deploy/env.prod，不提交进 git。

set -euo pipefail
cd "$(dirname "$0")"

usage() {
    cat <<'EOF'
用法：./deploy.sh <test|prod> <docker compose 参数…>

示例：
  ./deploy.sh test up -d
  ./deploy.sh prod up -d
  ./deploy.sh test logs -f api
  ./deploy.sh prod down

环境文件：
  deploy/env.test  → 从 deploy/env.test.example 复制
  deploy/env.prod  → 从 deploy/env.prod.example 复制
EOF
}

if [[ $# -lt 2 ]]; then
    usage
    exit 1
fi

env_name="$1"
shift

case "$env_name" in
test)
    project="infinite-canvas-test"
    compose_files=(-f docker-compose.yml -f deploy/compose.test.yml)
    env_file="deploy/env.test"
    ;;
prod)
    project="infinite-canvas-prod"
    compose_files=(-f docker-compose.yml)
    env_file="deploy/env.prod"
    ;;
*)
    usage
    exit 1
    ;;
esac

if [[ ! -f "$env_file" ]]; then
    echo "缺少 $env_file：先复制 ${env_file}.example 并填写" >&2
    exit 1
fi

cmd=(docker compose -p "$project" "${compose_files[@]}" --env-file "$env_file" "$@")
echo "+ ${cmd[*]}"
exec "${cmd[@]}"
