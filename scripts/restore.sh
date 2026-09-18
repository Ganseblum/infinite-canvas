#!/usr/bin/env bash
# 备份恢复：scripts/backup.sh 产出的三件套逆操作。
# 恢复前必须先停止写入方（docker compose stop api），数据库与媒体必须来自同一恢复点。
#
# 用法：
#   ./scripts/restore.sh --backup-dir backups/20260918-153000
#   ./scripts/restore.sh --backup-dir ... --db-container infinite-canvas-test-db-1
#   ./scripts/restore.sh --backup-dir ... --db-port 13306   # 走宿主机映射端口，需本机 mysql 客户端
#   ./scripts/restore.sh --backup-dir ... --yes             # 跳过确认
#
# 注意：
#   - 数据库恢复是覆盖式导入（先清空目标库再导入由 dump 内容决定，AutoMigrate 无回滚）；
#   - 媒体恢复是解包合并，不会删除目录里已有文件；建议先清空/移走目标媒体目录；
#   - env 密钥文件不会自动覆盖现有 env，复制为 <env-file>.restored-from-backup 供人工比对。

set -euo pipefail
cd "$(dirname "$0")/.."

BACKUP_DIR=""
DB_CONTAINER=""
DB_HOST="127.0.0.1"
DB_PORT=""
ENV_FILE=".env"
MEDIA_DIR="data/media"
ASSUME_YES=0

usage() { grep '^#   ' "$0" | sed 's/^#   //'; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --backup-dir)   BACKUP_DIR="$2"; shift 2 ;;
        --db-container) DB_CONTAINER="$2"; shift 2 ;;
        --db-host)      DB_HOST="$2"; shift 2 ;;
        --db-port)      DB_PORT="$2"; shift 2 ;;
        --env-file)     ENV_FILE="$2"; shift 2 ;;
        --media-dir)    MEDIA_DIR="$2"; shift 2 ;;
        --yes)          ASSUME_YES=1; shift ;;
        -h|--help)      usage ;;
        *) echo "未知参数: $1" >&2; usage ;;
    esac
done

[[ -n "$BACKUP_DIR" ]] || { echo "缺少 --backup-dir" >&2; usage; }
[[ -f "$BACKUP_DIR/db.sql.gz" ]] || { echo "备份目录缺少 db.sql.gz: $BACKUP_DIR" >&2; exit 1; }
[[ -f "$BACKUP_DIR/media.tar.gz" ]] || { echo "备份目录缺少 media.tar.gz: $BACKUP_DIR" >&2; exit 1; }
[[ -f "$BACKUP_DIR/env.copy" ]] || { echo "备份目录缺少 env.copy: $BACKUP_DIR" >&2; exit 1; }

env_value() { grep -E "^$1=" "$ENV_FILE" | tail -n1 | cut -d= -f2- | sed 's/^"//;s/"$//'; }

echo "== 恢复计划 =="
echo "  备份来源   : $BACKUP_DIR"
echo "  数据库方式 : $([[ -n "$DB_PORT" ]] && echo "端口 $DB_HOST:$DB_PORT（本机 mysql）" || echo "容器内 exec ${DB_CONTAINER:-infinite-canvas-db-1}")"
echo "  媒体目录   : $MEDIA_DIR (解包合并，不删除已有文件)"
echo "  env 文件   : 复制为 $ENV_FILE.restored-from-backup（不自动覆盖，请人工比对）"
echo "  恢复前请确认已停止写入方：docker compose stop api"
if [[ $ASSUME_YES -ne 1 ]]; then
    read -r -p "确认执行恢复？输入 yes 继续: " reply
    [[ "$reply" == "yes" ]] || { echo "已取消"; exit 1; }
fi

if [[ -n "$DB_PORT" ]]; then
    command -v mysql >/dev/null || { echo "本机没有 mysql 客户端，请改用 --db-container" >&2; exit 1; }
    DB_USER="$(env_value MYSQL_USER)"
    DB_PASS="$(env_value MYSQL_PASSWORD)"
    DB_NAME="$(env_value MYSQL_DATABASE)"
    [[ -n "$DB_USER" && -n "$DB_NAME" ]] || { echo "$ENV_FILE 缺少 MYSQL_USER / MYSQL_DATABASE" >&2; exit 1; }
    gunzip -c "$BACKUP_DIR/db.sql.gz" | mysql -h "$DB_HOST" -P "$DB_PORT" -u"$DB_USER" -p"$DB_PASS" "$DB_NAME"
else
    gunzip -c "$BACKUP_DIR/db.sql.gz" | docker exec -i "${DB_CONTAINER:-infinite-canvas-db-1}" sh -c \
        'exec mysql -u"$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE"'
fi

mkdir -p "$(dirname "$MEDIA_DIR")"
tar -xzf "$BACKUP_DIR/media.tar.gz" -C "$(dirname "$MEDIA_DIR")"
cp "$BACKUP_DIR/env.copy" "$ENV_FILE.restored-from-backup"
chmod 600 "$ENV_FILE.restored-from-backup"

echo "== 恢复完成 =="
echo "  数据库与媒体已导入；env 副本在 $ENV_FILE.restored-from-backup"
echo "  请比对后按需更新 $ENV_FILE，再 docker compose start api 并核对 /readyz"
