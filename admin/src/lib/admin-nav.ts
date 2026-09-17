// 菜单与权限点的唯一映射：侧边栏过滤、页面级守卫、首页重定向都从这里取，
// 避免同一份映射在布局、路由与页面里各写一遍、各漂移一次。
//
// 权限 key 与后端注册表（server/internal/authz/catalog.go）一一对应。
// 前端只负责隐藏入口，服务端是唯一权威：这里写错最多是多显示或少显示菜单，
// 真正的判断在服务端的 RequirePermission 中间件。
export const PERM = {
    statsRead: "stats.read",
    usersRead: "users.read",
    usersWrite: "users.write",
    usersCredits: "users.credits",
    modelsRead: "models.read",
    channelsRead: "channels.read",
    moderationRead: "moderation.read",
    packagesRead: "packages.read",
    ordersRead: "orders.read",
    settingsRead: "settings.read",
    settingsWrite: "settings.write",
    communityRead: "community.read",
    communityWrite: "community.write",
    rolesRead: "roles.read",
    rolesManage: "roles.manage",
    auditRead: "audit.read",
} as const;

export type AdminNavItem = {
    path: string;
    labelKey: string;
    // 拥有其中任意一个权限点即可见、可进：多数页面只对应一个权限点。
    anyOf: string[];
};

export const ADMIN_NAV_ITEMS: AdminNavItem[] = [
    { path: "/admin", labelKey: "admin.tabs.dashboard", anyOf: [PERM.statsRead] },
    { path: "/admin/users", labelKey: "admin.tabs.users", anyOf: [PERM.usersRead] },
    { path: "/admin/models", labelKey: "admin.tabs.models", anyOf: [PERM.modelsRead] },
    { path: "/admin/channels", labelKey: "admin.tabs.channels", anyOf: [PERM.channelsRead] },
    { path: "/admin/moderation", labelKey: "admin.tabs.moderation", anyOf: [PERM.moderationRead] },
    { path: "/admin/credit-packages", labelKey: "admin.tabs.packages", anyOf: [PERM.packagesRead] },
    { path: "/admin/orders", labelKey: "admin.tabs.orders", anyOf: [PERM.ordersRead] },
    // 系统页由四个 tab 组成，任一 tab 有权限就显示入口；tab 自身再按权限过滤。
    { path: "/admin/system", labelKey: "admin.tabs.system", anyOf: [PERM.settingsRead, PERM.rolesRead, PERM.communityRead, PERM.auditRead] },
];

export function hasAnyPermission(granted: string[], required: string[]) {
    return required.some((key) => granted.includes(key));
}

export function visibleNavItems(granted: string[]) {
    return ADMIN_NAV_ITEMS.filter((item) => hasAnyPermission(granted, item.anyOf));
}

// 首页重定向用：没有任何可见页面时返回 null（落到「无权限」页）。
export function firstAccessiblePath(granted: string[]): string | null {
    return visibleNavItems(granted)[0]?.path ?? null;
}

// 路由守卫用：路径所需的权限点直接取自同一份映射，路由与菜单不会各写一份。
export function permissionsForPath(path: string): string[] {
    return ADMIN_NAV_ITEMS.find((item) => item.path === path)?.anyOf ?? [];
}
