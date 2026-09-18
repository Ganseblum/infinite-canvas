package envload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("FOO_TEST=from-file\nBAR_TEST=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BAR_TEST", "from-env")
	t.Chdir(dir)

	if err := Load(".env"); err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}
	if got := os.Getenv("FOO_TEST"); got != "from-file" {
		t.Fatalf("文件中的变量未加载: %s", got)
	}
	if got := os.Getenv("BAR_TEST"); got != "from-env" {
		t.Fatalf("已存在的环境变量被覆盖: %s", got)
	}

	// 文件不存在时静默跳过
	if err := Load("no-such.env"); err != nil {
		t.Fatalf("缺失文件应静默跳过: %v", err)
	}
}

func TestLoadParentDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PARENT_TEST=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "server")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	if err := Load(".env"); err != nil {
		t.Fatalf("Load 返回错误: %v", err)
	}
	if got := os.Getenv("PARENT_TEST"); got != "1" {
		t.Fatalf("上级目录的 .env 未加载: %s", got)
	}
}
