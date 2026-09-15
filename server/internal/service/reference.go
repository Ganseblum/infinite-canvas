package service

import (
	"encoding/json"
	"regexp"
)

// storageKeyPattern 与服务端媒体接口、前端 storageKeyPattern 对齐。
var storageKeyPattern = regexp.MustCompile(`^(image|video|audio|file|video-reference|audio-reference):[A-Za-z0-9_-]{1,64}$`)

// ExtractStorageKeys 递归提取 JSON 里所有形如 `image:xxx` 的 storageKey。
// 画布节点的资源引用存在 metadata / data 的深层字段里，不做键名白名单，
// 避免字段重命名后引用收集静默漏掉，把仍在引用的文件当孤儿删掉。
func ExtractStorageKeys(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	var keys []string
	collectKeys(value, &keys)
	return keys
}

func collectKeys(value any, out *[]string) {
	switch typed := value.(type) {
	case string:
		if storageKeyPattern.MatchString(typed) {
			*out = append(*out, typed)
		}
	case []any:
		for _, item := range typed {
			collectKeys(item, out)
		}
	case map[string]any:
		for _, item := range typed {
			collectKeys(item, out)
		}
	}
}
