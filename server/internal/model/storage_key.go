package model

import "regexp"

// StorageKeyRe 是媒体存储键的统一格式契约（与前端 storageKeyPattern 对齐并收紧字符集），
// 画布/素材引用与媒体下发两侧共用，不匹配一律拒绝。
var StorageKeyRe = regexp.MustCompile(`^(image|video|audio|file|video-reference|audio-reference):[A-Za-z0-9_-]{1,64}$`)
