package model

import "time"

// StatZone 是用量统计统一使用的 UTC+8 时区；StatDateFormat 是统计日期列的格式。
// 用 FixedZone 而不是 LoadLocation，避免容器缺少 tzdata 时 panic。
var StatZone = time.FixedZone("UTC+8", 8*3600)

const StatDateFormat = "2006-01-02"
