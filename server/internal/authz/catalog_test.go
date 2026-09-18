package authz

import (
	"sort"
	"testing"
)

// goldenPermissionKeys 是权限点的金标列表，只能随功能评审显式修改。
// 注册表里删除、改名或交换含义都会让本测试失败：权限 key 一旦分配出去就是线上数据。
var goldenPermissionKeys = []string{
	"audit.read",
	"channels.read",
	"channels.write",
	"community.read",
	"community.write",
	"models.read",
	"models.write",
	"moderation.compensate",
	"moderation.read",
	"moderation.review",
	"orders.read",
	"orders.refund",
	"packages.read",
	"packages.write",
	"roles.manage",
	"roles.read",
	"settings.read",
	"settings.write",
	"stats.read",
	"stats.revenue",
	"stats.usage",
	"users.credits",
	"users.read",
	"users.write",
	"membership.read",
	"membership.write",
	"sso.read",
	"sso.write",
}

func TestRegistryMatchesGoldenKeys(t *testing.T) {
	got := Keys()
	want := append([]string(nil), goldenPermissionKeys...)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("权限点数量不一致: 注册表 %d 个, 金标 %d 个\n注册表=%v\n金标=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("权限点 key 不一致: 注册表 %q, 金标 %q\n注册表=%v\n金标=%v", got[i], want[i], got, want)
		}
	}
}

// TestRegistryCoversEveryPermissionConstant 保证每个权限常量都在注册表里，
// 避免新增常量却忘了登记（登记表才是路由注册与中间件的唯一依据）。
func TestRegistryCoversEveryPermissionConstant(t *testing.T) {
	constants := []string{
		PermStatsRead, PermStatsRevenue, PermStatsUsage,
		PermUsersRead, PermUsersWrite, PermUsersCredits,
		PermModelsRead, PermModelsWrite,
		PermChannelsRead, PermChannelsWrite,
		PermModerationRead, PermModerationReview, PermModerationCompensate,
		PermPackagesRead, PermPackagesWrite,
		PermOrdersRead, PermOrdersRefund,
		PermSettingsRead, PermSettingsWrite,
		PermCommunityRead, PermCommunityWrite,
		PermRolesRead, PermRolesManage,
		PermAuditRead,
		PermMembershipRead, PermMembershipWrite,
		PermSSORead, PermSSOWrite,
	}
	for _, key := range constants {
		if !IsKnown(key) {
			t.Fatalf("权限常量 %s 未登记到注册表", key)
		}
	}
	if len(constants) != len(goldenPermissionKeys) {
		t.Fatalf("权限常量数量 %d 与金标数量 %d 不一致", len(constants), len(goldenPermissionKeys))
	}
}

func TestRegistryDefinitionIsComplete(t *testing.T) {
	if err := validateRegistry(); err != nil {
		t.Fatalf("注册表自检失败: %v", err)
	}
	seen := map[string]bool{}
	for _, def := range Permissions() {
		if seen[def.Key] {
			t.Fatalf("权限点重复登记: %s", def.Key)
		}
		seen[def.Key] = true
		if def.Sort <= 0 {
			t.Fatalf("权限点 %s 缺少排序", def.Key)
		}
		if ModuleLabel(def.Module) == def.Module {
			t.Fatalf("权限点 %s 的模块 %s 缺少展示名", def.Key, def.Module)
		}
	}
	if len(registry) != 28 {
		t.Fatalf("权限点总数应为 28, got %d", len(registry))
	}
	if IsKnown("stats.unknown") || IsKnown("") {
		t.Fatalf("未注册的 key 不允许通过 IsKnown")
	}
}
