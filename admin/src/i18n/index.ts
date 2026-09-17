import i18n from "@/i18n";

import enUS from "@admin/i18n/locales/en-US";
import zhCN from "@admin/i18n/locales/zh-CN";

// 复用 web 的 i18n 实例（模块级单例），而不是再 init 一份：
// lib/api-error.ts 直接引用 @/i18n，如果 admin 自己 init 一个新实例，t() 与 getApiErrorMessage
// 会绑到不同实例，语言切换只作用于其中一个。这里只把自己的命名空间并进同一个实例。
//
// 命名空间分层：
//   - 默认 translation 命名空间：沿用 web 语言包里的 admin.* 文案，迁移过来的 8 个页面照旧 t("admin.xxx")；
//   - admin 命名空间（本目录 locales）：只放 admin 应用外壳自己的文案（侧边栏、说明页等）。
i18n.addResourceBundle("zh-CN", "admin", zhCN, true, false);
i18n.addResourceBundle("en-US", "admin", enUS, true, false);

// 补 web 语言包里缺失的 admin.tabs.system（现有顶栏 tab 会直接把 key 显示给用户）。
// deep + 不覆盖：只填缺口，web 语言包以后补上这一条时会被保留。
i18n.addResourceBundle("zh-CN", "translation", { admin: { tabs: { system: "系统" } } }, true, false);
i18n.addResourceBundle("en-US", "translation", { admin: { tabs: { system: "System" } } }, true, false);
