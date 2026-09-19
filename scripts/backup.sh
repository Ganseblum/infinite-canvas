#!/usr/bin/env bash
# 备份三件套：MySQL 数据库（mysqldump --single-transaction）+ 媒体目录（tar）+ env 密钥文件副本。
# 输出到带时间戳的目录，例如 backups/20260918-153000/{db.sql.gz, media.tar.gz, env.copy}。
#
# 用法：
#   ./scripts/backup.sh                              # 容器内 exec 方式（默认容器 infinite-canvas-db-1）
#   ./scripts/backup.sh --db-container infinite-canvas-test-db-1
#   ./scripts/backup.sh --db-port 13306              # 走宿主机映射端口，需本机 mysqldump 客户端
#   ./scripts/backup.sh --env-file deploy/env.test --media-dir /srv/infinite-canvas/data/media
#   ./scripts/backup.sh --yes                        # 跳过确认（cron 用）
#
# 依赖：docker（容器方式）或本机 mysqldump（端口方式）；tar、gzip。
# 每次升级前必须先执行备份：AutoMigrate 没有结构回滚，见 deploy/README.md「备份与恢复演练」。

set -euo pipefail
cd "$(dirname "$0")/.."

DB_CONTAINER=""
DB_HOST="127.0.0.1"
DB_PORT=""
ENV_FILE=".env"
MEDIA_DIR="data/media"
OUT_DIR="backups"
ASSUME_YES=0

usage() { grep '^#   ' "$0" | sed 's/^#   //'; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --db-container) DB_CONTAINER="$2"; shift 2 ;;
        --db-host)      DB_HOST="$2"; shift 2 ;;
        --db-port)      DB_PORT="$2"; shift 2 ;;
        --env-file)     ENV_FILE="$2"; shift 2 ;;
        --media-dir)    MEDIA_DIR="$2"; shift 2 ;;
        --out-dir)      OUT_DIR="$2"; shift 2 ;;
        --yes)          ASSUME_YES=1; shift ;;
        -h|--help)      usage ;;
        *) echo "未知参数: $1" >&2; usage ;;
    esac
done

[[ -f "$ENV_FILE" ]] || { echo "env 文件不存在: $ENV_FILE（env 密钥属于备份三件套之一）" >&2; exit 1; }
[[ -d "$MEDIA_DIR" ]] || { echo "媒体目录不存在: $MEDIA_DIR" >&2; exit 1; }

env_value() { grep -E "^$1=" "$ENV_FILE" | tail -n1 | cut -d= -f2- | sed 's/^"//;s/"$//'; }

BACKUP_DIR="${OUT_DIR}/$(date +%Y%m%d-%H%M%S)"

echo "== 备份计划 =="
echo "  数据库方式 : $([[ -n "$DB_PORT" ]] && echo "端口 $DB_HOST:$DB_PORT（本机 mysqldump）" || echo "容器内 exec ${DB_CONTAINER:-infinite-canvas-db-1}")"
echo "  媒体目录   : $MEDIA_DIR (读取)"
echo "  env 文件   : $ENV_FILE (复制，含密钥，权限 600)"
echo "  输出目录   : $BACKUP_DIR/ (写入 db.sql.gz、media.tar.gz、env.copy)"
if [[ $ASSUME_YES -ne 1 ]]; then
    read -r -p "确认执行备份？输入 yes 继续: " reply
    [[ "$reply" == "yes" ]] || { echo "已取消"; exit 1; }
fi

mkdir -p "$BACKUP_DIR"

if [[ -n "$DB_PORT" ]]; then
    command -v mysqldump >/dev/null || { echo "本机没有 mysqldump，请改用 --db-container 或安装 mysql 客户端" >&2; exit 1; }
    DB_USER="$(env_value MYSQL_USER)"
    DB_PASS="$(env_value MYSQL_PASSWORD)"
    DB_NAME="$(env_value MYSQL_DATABASE)"
    [[ -n "$DB_USER" && -n "$DB_NAME" ]] || { echo "$ENV_FILE 缺少 MYSQL_USER / MYSQL_DATABASE" >&2; exit 1; }
    mysqldump --single-transaction --no-tablespaces --set-gtid-purged=OFF \
        -h "$DB_HOST" -P "$DB_PORT" -u"$DB_USER" -p"$DB_PASS" "$DB_NAME" | gzip > "$BACKUP_DIR/db.sql.gz"
else
    docker exec "${DB_CONTAINER:-infinite-canvas-db-1}" sh -c \
        'exec mysqldump --single-transaction --no-tablespaces --set-gtid-purged=OFF -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"' \
        | gzip > "$BACKUP_DIR/db.sql.gz"
fi

tar -czf "$BACKUP_DIR/media.tar.gz" -C "$(dirname "$MEDIA_DIR")" "$(basename "$MEDIA_DIR")"
cp "$ENV_FILE" "$BACKUP_DIR/env.copy"
chmod 600 "$BACKUP_DIR/env.copy"

echo "== 备份完成 =="
ls -lh "$BACKUP_DIR"
