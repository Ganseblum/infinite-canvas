package errs

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestIsDuplicateKey(t *testing.T) {
	if IsDuplicateKey(nil) {
		t.Fatal("nil 不应判为唯一冲突")
	}
	if IsDuplicateKey(errors.New("connection refused")) {
		t.Fatal("普通错误不应判为唯一冲突")
	}
	if !IsDuplicateKey(&mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'x' for key 'users.idx_users_email'"}) {
		t.Fatal("MySQL 1062 应判为唯一冲突")
	}
	if IsDuplicateKey(&mysql.MySQLError{Number: 1146, Message: "Table 'x' doesn't exist"}) {
		t.Fatal("非 1062 的 MySQL 错误不应判为唯一冲突")
	}
	if !IsDuplicateKey(errors.New("UNIQUE constraint failed: users.email")) {
		t.Fatal("SQLite 唯一冲突文案应判为唯一冲突")
	}
}

func TestUniqueViolationColumn(t *testing.T) {
	// MySQL：按报文里的索引名映射列；重复值含列名字样不得影响判定（#45b 误判回归）
	mysqlErr := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'i-am-email-fan' for key 'users.idx_users_username'"}
	if col := UniqueViolationColumn(mysqlErr); col != "username" {
		t.Fatalf("重复值含 email 字样时应按索引名判为 username, got %q", col)
	}
	if col := UniqueViolationColumn(&mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'a@b.c' for key 'users.idx_users_email'"}); col != "email" {
		t.Fatalf("应按索引名映射到 email, got %q", col)
	}
	// 兼容不带表前缀的旧版报文
	if col := UniqueViolationColumn(&mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'a@b.c' for key 'idx_users_email'"}); col != "email" {
		t.Fatalf("不带表前缀的索引名也应映射, got %q", col)
	}
	// 未登记的索引按未知错误处理
	if col := UniqueViolationColumn(&mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'x' for key 'orders.idx_orders_no'"}); col != "" {
		t.Fatalf("未登记索引应返回空串, got %q", col)
	}
	if col := UniqueViolationColumn(&mysql.MySQLError{Number: 1146, Message: "Table doesn't exist"}); col != "" {
		t.Fatalf("非 1062 应返回空串, got %q", col)
	}
	// SQLite（测试库）：直接解析列名
	if col := UniqueViolationColumn(errors.New("UNIQUE constraint failed: users.username")); col != "username" {
		t.Fatalf("SQLite 报文应解析出 username, got %q", col)
	}
	if col := UniqueViolationColumn(errors.New("UNIQUE constraint failed: users.email, users.username")); col != "email" {
		t.Fatalf("多列冲突取第一列, got %q", col)
	}
	if col := UniqueViolationColumn(nil); col != "" {
		t.Fatalf("nil 应返回空串, got %q", col)
	}
	if col := UniqueViolationColumn(fmt.Errorf("connection refused")); col != "" {
		t.Fatalf("普通错误应返回空串, got %q", col)
	}
}
