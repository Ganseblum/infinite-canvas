import { ADMIN_PRODUCTS, CURRENT_PRODUCT_KEY } from "@admin/lib/products";

// 菜单与权限点的唯一映射：侧边栏过滤、页面级守卫、首页重定向都从这里取，
// 避免同一份映射在布局、路由与页面里各写一遍、各漂移一次。
//
// 侧边栏是两层菜单：父级 = 产品（见 ADMIN_NAV_PARENTS），已接入产品的子项 = 全部管理菜单；
// 「模型」子项下还有能力快捷入口（capabilityChildren），各能力的数量由侧边栏自行查询填充。
//
// 权限 key 与后端注册表（server/internal/authz/catalog.go）一一对应。
// 前端只负责隐藏入口，服务端是唯一权威：这里写错最多是多显示或少显示菜单，
// 真正的判断在服务端的 RequirePermission 中间件。
export const PERM = {
    statsRead: "stats.read",
    statsUsage: "stats.usage",
    usersRead: "users.read",
    usersWrite: "users.write",
    usersCredits: "users.credits",
    membershipRead: "membership.read",
    membershipWrite: "membership.write",
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

// 模型能力快捷入口的能力值，与模型页 Segmented、服务端 catalog 的能力取值一致。
export type AdminNavCapability = "image" | "video" | "audio" | "text";

// 「模型」下的第二层子菜单项：path 带查询参数，模型页据此预选筛选，文案 key 归侧边栏 i18n。
export type AdminNavCapabilityItem = {
    capability: AdminNavCapability;
    path: string;
    labelKey: string;
};

export type AdminNavItem = {
    path: string;
    labelKey: string;
    // 拥有其中任意一个权限点即可见、可进：多数页面只对应一个权限点。
    anyOf: string[];
    // 第二层子菜单（目前只有「模型」）：是否显示跟随本项的 anyOf 权限过滤。
    capabilityChildren?: AdminNavCapabilityItem[];
};

export const ADMIN_NAV_ITEMS: AdminNavItem[] = [
    { path: "/admin", labelKey: "admin.tabs.dashboard", anyOf: [PERM.statsRead] },
    { path: "/admin/analytics", labelKey: "admin.tabs.analytics", anyOf: [PERM.statsUsage] },
    { path: "/admin/users", labelKey: "admin.tabs.users", anyOf: [PERM.usersRead] },
    { path: "/admin/membership", labelKey: "admin.tabs.membership", anyOf: [PERM.membershipRead] },
    {
        path: "/admin/models",
        labelKey: "admin.tabs.models",
        anyOf: [PERM.modelsRead],
        // 能力快捷入口：点击带查询参数跳模型页并预选筛选；数量由侧边栏查询后拼进文案。
        capabilityChildren: [
            { capability: "image", path: "/admin/models?capability=image", labelKey: "admin.tabs.modelCapabilities.image" },
            { capability: "video", path: "/admin/models?capability=video", labelKey: "admin.tabs.modelCapabilities.video" },
            { capability: "audio", path: "/admin/models?capability=audio", labelKey: "admin.tabs.modelCapabilities.audio" },
            { capability: "text", path: "/admin/models?capability=text", labelKey: "admin.tabs.modelCapabilities.text" },
        ],
    },
    { path: "/admin/channels", labelKey: "admin.tabs.channels", anyOf: [PERM.channelsRead] },
    { path: "/admin/moderation", labelKey: "admin.tabs.moderation", anyOf: [PERM.moderationRead] },
    { path: "/admin/credit-packages", labelKey: "admin.tabs.packages", anyOf: [PERM.packagesRead] },
    { path: "/admin/orders", labelKey: "admin.tabs.orders", anyOf: [PERM.ordersRead] },
    // 系统页由四个 tab 组成，任一 tab 有权限就显示入口；tab 自身再按权限过滤。
    { path: "/admin/system", labelKey: "admin.tabs.system", anyOf: [PERM.settingsRead, PERM.rolesRead, PERM.communityRead, PERM.auditRead] },
];

// 侧边栏父级 = 产品：key 对应 lib/products.ts 的注册表，产品名、状态等文案与状态都从注册表取。
// 已接入产品的子项是全部管理菜单；规划中产品没有子项，整项可点，落到 /admin/product/:key 占位页。
// planned 父级直接从注册表派生：以后注册表加产品，侧边栏父级菜单跟着长出来。
export type AdminNavParent = { key: string; children: AdminNavItem[] };

export const ADMIN_NAV_PARENTS: AdminNavParent[] = [
    { key: CURRENT_PRODUCT_KEY, children: ADMIN_NAV_ITEMS },
    ...ADMIN_PRODUCTS.filter((product) => product.status === "planned").map((product) => ({ key: product.key, children: [] as AdminNavItem[] })),
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

// 侧边栏 selectedKeys：模型页按 capability 参数选中对应能力子项（无参数或参数未知时选「模型」本身，
// 高亮 SubMenu 标题），其余页面沿用路径前缀匹配；占位页等映射外的路径返回 null，不选中任何菜单项。
export function activeNavKey(pathname: string, search: string): string | null {
    const modelsItem = ADMIN_NAV_ITEMS.find((item) => item.capabilityChildren?.length);
    if (modelsItem && pathname === modelsItem.path) {
        const capability = new URLSearchParams(search).get("capability");
        const child = capability ? modelsItem.capabilityChildren?.find((item) => item.capability === capability) : undefined;
        return child?.path ?? modelsItem.path;
    }
    const item = ADMIN_NAV_ITEMS.find((candidate) => candidate.path === pathname || (candidate.path !== "/admin" && pathname.startsWith(candidate.path)));
    return item?.path ?? null;
}

// 侧边栏 openKeys：停在模型页（含带能力参数）时，产品父级与模型两级 SubMenu 必须展开，
// 否则选中的能力子项不可见；其余路径返回空数组，展开状态完全交给用户自己控制。
export function requiredOpenKeys(pathname: string): string[] {
    const modelsItem = ADMIN_NAV_ITEMS.find((item) => item.capabilityChildren?.length);
    if (!modelsItem || pathname !== modelsItem.path) return [];
    const parent = ADMIN_NAV_PARENTS.find((group) => group.children.includes(modelsItem));
    return parent ? [parent.key, modelsItem.path] : [modelsItem.path];
}
