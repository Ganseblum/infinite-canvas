#!/usr/bin/env bash
# 按环境运行 docker compose：测试环境与正式环境用不同项目名，
# 容器名、网络、数据卷（数据库、媒体）三样都会自动隔离，不会互相覆盖。
#
#   ./deploy.sh test build             构建测试环境镜像（tag 来自 env 文件的 APP_IMAGE / API_IMAGE）
#   ./deploy.sh test up -d             启动测试环境（宿主机端口由 env 文件的 APP_PORT 指定，仅绑回环）
#   ./deploy.sh prod build             构建正式环境镜像（:prod）
#   ./deploy.sh prod up -d             启动正式环境
#   ./deploy.sh test logs -f api       看测试环境 api 日志
#   ./deploy.sh test down              停止测试环境（数据卷保留）
#
# 更新流程：git pull → ./deploy.sh <env> build → ./deploy.sh <env> up -d。
# up 会固定 --force-recreate app api：app 容器内 nginx 启动时解析并缓存 api 容器 IP，
# api 重建后必须连带重建 app，否则 /api 反代仍指向旧 IP 导致 502；db 不受影响。
#
# 依赖：docker compose（v2）。环境变量文件放在 deploy/env.test、deploy/env.prod，不提交进 git。

set -euo pipefail
cd "$(dirname "$0")"

usage() {
    cat <<'EOF'
用法：./deploy.sh <test|prod> <build|docker compose 参数…>

示例：
  ./deploy.sh test build
  ./deploy.sh test up -d
  ./deploy.sh prod build
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

action="$1"
shift

case "$action" in
build)
    # 构建并按 env 文件里的 APP_IMAGE / API_IMAGE 打 tag（:test / :prod）
    cmd=(docker compose -p "$project" "${compose_files[@]}" --env-file "$env_file" build "$@")
    ;;
up)
    # app 容器内 nginx 启动时缓存 api 容器 IP：api 重建后必须连带重建 app，
    # 固定对两者 force-recreate；db 只在缺失或配置变化时被 compose 处理。
    cmd=(docker compose -p "$project" "${compose_files[@]}" --env-file "$env_file" up "$@" --force-recreate app api)
    ;;
*)
    cmd=(docker compose -p "$project" "${compose_files[@]}" --env-file "$env_file" "$action" "$@")
    ;;
esac

echo "+ ${cmd[*]}"
exec "${cmd[@]}"
