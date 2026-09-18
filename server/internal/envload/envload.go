// Package envload 在进程启动前加载仓库根目录或 server/ 目录下的 .env 文件。
// 容器部署没有 .env，静默跳过；已导出的环境变量优先，不被文件覆盖。
package envload

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// Load 加载 path 指向的 env 文件：存在则加载（已存在的环境变量不覆盖），
// 不存在则静默跳过。path 为相对路径时依次尝试当前工作目录与上级目录，
// 兼容「仓库根 .env」与「cd server && go run 时的 server/.env」两种启动方式。
func Load(path string) error {
	candidates := []string{path}
	if !filepath.IsAbs(path) {
		candidates = append(candidates, filepath.Join("..", path))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return godotenv.Load(candidate)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
