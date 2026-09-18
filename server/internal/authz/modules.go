package authz

// ModuleInfo 是按模块聚合的权限清单：/admin/meta 的 modules 字段与角色编辑共用这一形状。
type ModuleInfo struct {
	Key         string          `json:"key"`
	Label       string          `json:"label"`
	Permissions []PermissionDef `json:"permissions"`
}

// ModulesFor 按已授予的权限点过滤模块清单：只返回调用者至少拥有一个权限的模块，
// 模块内也只保留已授予的权限点。系统角色的 granted 是全部注册权限，因此返回完整清单。
// 顺序与注册表一致（模块按其最小 Sort 排列）。
func ModulesFor(granted []string) []ModuleInfo {
	set := make(map[string]bool, len(granted))
	for _, key := range granted {
		set[key] = true
	}
	order := []string{}
	byModule := map[string][]PermissionDef{}
	for _, def := range registry {
		if !set[def.Key] {
			continue
		}
		if _, ok := byModule[def.Module]; !ok {
			order = append(order, def.Module)
		}
		byModule[def.Module] = append(byModule[def.Module], def)
	}
	out := make([]ModuleInfo, 0, len(order))
	for _, key := range order {
		out = append(out, ModuleInfo{
			Key:         key,
			Label:       moduleLabels[key],
			Permissions: byModule[key],
		})
	}
	return out
}
