// Package authz 定义后台 RBAC 的权限点注册表、角色写入助手与启动同步。
//
// 目录在代码、分配在数据：permissions 表只是本包注册表在库里的投影，
// 界面可配置的只有「角色 ↔ 权限」分配。任何权限点的增删改都必须改本包，
// 并由金标测试（catalog_test.go）兜住，避免 key 被悄悄改名或删除。
package authz

import (
	"fmt"
	"sort"
)

// SystemRoleKey 是内置系统角色的标识。它隐式拥有全部权限（短路由，不查 role_permissions），
// 不可删除、不可改权限、key 不可改，是防锁死的根基。
const SystemRoleKey = "admin"

// 权限点常量：路由注册只允许使用这里定义的常量，未知 key 在启动期即 panic。
const (
	PermStatsRead            = "stats.read"
	PermStatsRevenue         = "stats.revenue"
	PermStatsUsage           = "stats.usage"
	PermUsersRead            = "users.read"
	PermUsersWrite           = "users.write"
	PermUsersCredits         = "users.credits"
	PermModelsRead           = "models.read"
	PermModelsWrite          = "models.write"
	PermChannelsRead         = "channels.read"
	PermChannelsWrite        = "channels.write"
	PermModerationRead       = "moderation.read"
	PermModerationReview     = "moderation.review"
	PermModerationCompensate = "moderation.compensate"
	PermPackagesRead         = "packages.read"
	PermPackagesWrite        = "packages.write"
	PermOrdersRead           = "orders.read"
	PermOrdersRefund         = "orders.refund"
	PermSettingsRead         = "settings.read"
	PermSettingsWrite        = "settings.write"
	PermCommunityRead        = "community.read"
	PermCommunityWrite       = "community.write"
	PermRolesRead            = "roles.read"
	PermRolesManage          = "roles.manage"
	PermAuditRead            = "audit.read"
)

// PermissionDef 是一个权限点在代码里的定义。
type PermissionDef struct {
	Key         string
	Module      string
	Name        string
	Description string
	Sort        int
}

// moduleLabels 是模块的展示名，用于后台按模块分组渲染权限清单。
var moduleLabels = map[string]string{
	"stats":      "数据统计",
	"users":      "用户管理",
	"models":     "模型与折扣",
	"channels":   "上游渠道",
	"moderation": "内容审核",
	"packages":   "充值档位",
	"orders":     "订单",
	"settings":   "站点设置",
	"community":  "社区",
	"roles":      "角色权限",
	"audit":      "审计日志",
}

// registry 是权限点注册表，按模块与 Sort 升序排列。修改这里必须同步更新金标测试。
var registry = []PermissionDef{
	{Key: PermStatsRead, Module: "stats", Name: "查看总览", Description: "查看用户、生成、存储与订单总量", Sort: 10},
	{Key: PermStatsRevenue, Module: "stats", Name: "查看营收", Description: "查看充值金额、付费用户数与转化率", Sort: 20},
	{Key: PermStatsUsage, Module: "stats", Name: "查看用量分析", Description: "查看 AI 生成的按天用量与模型、能力、规格、用户消费分布", Sort: 25},
	{Key: PermUsersRead, Module: "users", Name: "查看用户", Description: "查看用户列表与用户详情", Sort: 30},
	{Key: PermUsersWrite, Module: "users", Name: "管理用户", Description: "修改用户状态、重置密码、重算用量、回收媒体", Sort: 40},
	{Key: PermUsersCredits, Module: "users", Name: "调整点数", Description: "增减用户点数余额，属资金性质操作", Sort: 50},
	{Key: PermModelsRead, Module: "models", Name: "查看模型", Description: "查看模型目录与折扣活动", Sort: 60},
	{Key: PermModelsWrite, Module: "models", Name: "管理模型", Description: "新增、修改、删除模型与折扣活动", Sort: 70},
	{Key: PermChannelsRead, Module: "channels", Name: "查看渠道", Description: "查看上游渠道配置", Sort: 80},
	{Key: PermChannelsWrite, Module: "channels", Name: "管理渠道", Description: "新增、修改、删除渠道与凭据", Sort: 90},
	{Key: PermModerationRead, Module: "moderation", Name: "查看审核", Description: "查看审核记录、统计与隔离区预览", Sort: 100},
	{Key: PermModerationReview, Module: "moderation", Name: "人工复核", Description: "人工改判审核记录，覆盖机器结论", Sort: 110},
	{Key: PermModerationCompensate, Module: "moderation", Name: "审核补偿", Description: "对误判记录补发点数", Sort: 120},
	{Key: PermPackagesRead, Module: "packages", Name: "查看档位", Description: "查看充值档位", Sort: 130},
	{Key: PermPackagesWrite, Module: "packages", Name: "管理档位", Description: "新增、修改充值档位", Sort: 140},
	{Key: PermOrdersRead, Module: "orders", Name: "查看订单", Description: "查看订单列表与支付状态", Sort: 150},
	{Key: PermOrdersRefund, Module: "orders", Name: "重试退款", Description: "重试退款请求，属资金性质操作", Sort: 160},
	{Key: PermSettingsRead, Module: "settings", Name: "查看站点设置", Description: "查看站点公告、功能开关与限额", Sort: 170},
	{Key: PermSettingsWrite, Module: "settings", Name: "修改站点设置", Description: "修改站点公告、功能开关与限额", Sort: 180},
	{Key: PermCommunityRead, Module: "community", Name: "查看社区", Description: "查看社区作品与举报", Sort: 190},
	{Key: PermCommunityWrite, Module: "community", Name: "管理社区", Description: "下架作品、处理举报", Sort: 200},
	{Key: PermRolesRead, Module: "roles", Name: "查看角色", Description: "查看角色、权限点清单与后台成员", Sort: 210},
	{Key: PermRolesManage, Module: "roles", Name: "管理角色", Description: "增删改角色、分配权限、调整用户角色，属特权管理", Sort: 220},
	{Key: PermAuditRead, Module: "audit", Name: "查看审计日志", Description: "查看管理操作审计", Sort: 230},
}

var registryIndex = func() map[string]PermissionDef {
	index := make(map[string]PermissionDef, len(registry))
	for _, def := range registry {
		index[def.Key] = def
	}
	return index
}()

// retiredKeys 是权限点改名的逃生门：旧 key → 新 key。
// 启动同步会把旧 key 的角色分配幂等复制到新 key，再给旧 key 标废弃；
// 废弃 key 禁止复用给不同含义。
var retiredKeys = map[string]string{}

// Permissions 返回注册表里的全部权限点定义（按模块与排序）。
func Permissions() []PermissionDef {
	out := make([]PermissionDef, len(registry))
	copy(out, registry)
	return out
}

// Keys 返回注册表里全部权限点的 key，已排序。
func Keys() []string {
	keys := make([]string, 0, len(registry))
	for _, def := range registry {
		keys = append(keys, def.Key)
	}
	sort.Strings(keys)
	return keys
}

// IsKnown 判断 key 是否在代码注册表中。中间件与启动同步都以它为唯一依据。
func IsKnown(key string) bool {
	_, ok := registryIndex[key]
	return ok
}

// Lookup 返回权限点定义。
func Lookup(key string) (PermissionDef, bool) {
	def, ok := registryIndex[key]
	return def, ok
}

// ModuleLabel 返回模块展示名，未登记的模块回落为模块 key 本身。
func ModuleLabel(module string) string {
	if label, ok := moduleLabels[module]; ok {
		return label
	}
	return module
}

// validateRegistry 在包初始化时自检注册表本身没有重复或空字段。
func validateRegistry() error {
	seen := make(map[string]struct{}, len(registry))
	for _, def := range registry {
		if def.Key == "" || def.Module == "" || def.Name == "" {
			return fmt.Errorf("权限点定义缺少 key/module/name: %+v", def)
		}
		if _, dup := seen[def.Key]; dup {
			return fmt.Errorf("权限点 key 重复: %s", def.Key)
		}
		seen[def.Key] = struct{}{}
		if _, ok := moduleLabels[def.Module]; !ok {
			return fmt.Errorf("权限点 %s 的模块 %s 未登记展示名", def.Key, def.Module)
		}
	}
	return nil
}
