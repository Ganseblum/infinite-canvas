package errs

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// uniqueIndexColumns 是唯一索引名到业务列的映射，索引名来自 model 里的 uniqueIndex 标签
// （GORM 默认命名为 idx_表名_列名）。MySQL 1062 报文只给索引名，必须靠它映射回列；
// 不能拿报错文本去匹配「重复值」——值本身含列名字样时（如用户名含 email 字样）会误判。
var uniqueIndexColumns = map[string]string{
	"idx_users_email":    "email",
	"idx_users_username": "username",
	"idx_blog_posts_slug":   "slug",
	"idx_blog_topics_slug":  "slug",
}

// IsDuplicateKey 判断错误是否为唯一索引冲突：MySQL 按 1062 错误码判定，
// 测试用 SQLite 按报错文案判定。
func IsDuplicateKey(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}

// UniqueViolationColumn 返回唯一冲突命中的业务列：
// MySQL 从报文 Duplicate entry 'x' for key 'users.idx_users_email' 里取索引名查映射，
// SQLite 直接解析 UNIQUE constraint failed: users.email 里的列名。
// 非唯一冲突或索引未登记时返回空串，调用方按未知错误处理（fail closed）。
func UniqueViolationColumn(err error) string {
	if err == nil {
		return ""
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		if mysqlErr.Number != 1062 {
			return ""
		}
		_, indexName, ok := strings.Cut(mysqlErr.Message, "for key '")
		if !ok {
			return ""
		}
		indexName, _, _ = strings.Cut(indexName, "'")
		// 兼容带表前缀（users.idx_users_email）与不带前缀（idx_users_email）两种报文。
		if _, name, found := strings.Cut(indexName, "."); found {
			indexName = name
		}
		return uniqueIndexColumns[indexName]
	}
	_, rest, ok := strings.Cut(strings.ToLower(err.Error()), "unique constraint failed:")
	if !ok {
		return ""
	}
	first, _, _ := strings.Cut(rest, ",")
	if _, col, found := strings.Cut(strings.TrimSpace(first), "."); found {
		return col
	}
	return strings.TrimSpace(first)
}
