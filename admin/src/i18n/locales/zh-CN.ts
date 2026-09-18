import i18n from "@/i18n";

// admin 应用外壳自己的文案，与 web 语言包里的 admin.* 键组分属不同命名空间：
// t("admin.title") 读默认 translation 命名空间里的键组，useTranslation("admin") 读本文件。
export default {
    docTitle: "管理后台 · 优刻",
    previewFailed: "隔离原件预览加载失败，请刷新重试。",
    // 侧边栏账号区显示的当前后台角色：菜单是按这个角色的权限过滤过的，写出来少一点「菜单怎么少了」的困惑。
    currentRole: "当前角色：{{role}}",
    login: {
        title: "管理员登录",
        description: "请使用管理员账号登录管理后台。",
        account: "账号",
        accountPlaceholder: "邮箱或用户名",
        password: "密码",
        passwordPlaceholder: "请输入密码",
        accountRequired: "请输入账号",
        passwordRequired: "请输入密码",
        submit: "登录",
    },
    // 会话跨应用共用：refresh cookie 是 host-only 挂在 API 域（sim-art.youc.online）的 /api/auth 下，
    // 两个域的前端打的是同一个 API 域，拿到的是同一个登录态——登录与登出都必须向用户讲清楚。
    sharedSession: {
        loginNote: "登录态与主站共用：在这里登录后，主站也会同时进入登录状态。",
        logoutNote: "登录态与主站共用：退出后台会同时退出主站。",
    },
    // 顶栏环境标识：正式环境恒显红色角标（后台操作大多不可逆），测试/开发沿用主站的黄色小标签。
    envBadge: {
        production: "正式环境",
        productionTitle: "当前是正式环境，请谨慎操作",
        test: "测试环境",
        dev: "本地开发",
        nonProductionTitle: "当前访问的不是正式环境",
    },
    forbidden: {
        title: "当前账号没有后台访问权限",
        description: "当前账号 {{email}} 可以正常使用主站，但没有管理后台的访问权限。需要后台权限请联系管理员开通，或换一个管理员账号登录。",
    },
    // 强制改密页：must_change_password 置位后服务端只放行登出/刷新/改密，这里是唯一落点，
    // 只提供改密与退出两条出路；改密成功会清掉标志并自动进入后台，不需要重新登录。
    changePassword: {
        title: "请先修改初始密码",
        description: "为了账号安全，当前账号需要先修改密码才能继续使用管理后台。修改成功后会自动进入后台，无需重新登录。",
        oldPassword: "当前密码",
        oldPasswordPlaceholder: "请输入当前密码",
        oldPasswordRequired: "请输入当前密码",
        oldPasswordWrong: "当前密码不正确",
        newPassword: "新密码",
        newPasswordPlaceholder: "请输入新密码",
        newPasswordRequired: "请输入新密码",
        newPasswordLength: "新密码长度需在 8-72 位之间",
        confirmPassword: "确认新密码",
        confirmPasswordPlaceholder: "请再次输入新密码",
        confirmPasswordRequired: "请再次输入新密码",
        confirmPasswordMismatch: "两次输入的新密码不一致",
        submit: "修改密码并进入后台",
    },
    // 页面级守卫拦下无权限的路径时落到这里；403 的接口错误也用同一套文案。
    noPermission: {
        title: "当前角色没有该页面的权限",
        description: "当前角色「{{role}}」未包含访问这个页面所需的权限。左侧菜单只显示有权限的页面，需要更多权限请联系系统管理员调整角色。",
        descriptionNoRole: "当前账号还没有被分配后台角色。左侧菜单只显示有权限的页面，需要权限请联系系统管理员。",
        back: "去有权限的页面",
    },
    permissionDenied: {
        title: "没有访问权限",
        description: "当前角色没有调用这个接口的权限，重试不会改变结果。菜单和入口已按权限隐藏，需要更多权限请联系系统管理员调整角色。",
    },
    roles: {
        tab: "角色与成员",
        hint: "角色决定后台能看到与能操作的范围；权限点由服务端代码注册表定义，界面只负责把权限分配给角色，不能增删权限点本身。",
        readOnlyHint: "当前角色只有查看权限，不能修改。",
        create: "新建角色",
        createTitle: "新建角色",
        editTitle: "编辑角色：{{name}}",
        created: "角色已创建",
        updated: "角色已保存",
        deleted: "角色已删除",
        system: "系统角色",
        custom: "自定义角色",
        allPermissions: "全部权限",
        view: "查看",
        edit: "编辑",
        delete: "删除",
        deleteConfirm: "删除角色「{{name}}」？删除后无法恢复。",
        systemNoDelete: "系统角色不可删除",
        hasMembers: "该角色仍有成员，需先调整成员角色",
        systemNoEdit: "系统角色隐式拥有全部权限，权限不可编辑，只能改显示名与说明。",
        membersTitle: "系统角色成员",
        columns: {
            role: "角色",
            description: "说明",
            type: "类型",
            permissions: "权限数",
            members: "成员数",
            actions: "操作",
        },
        fields: {
            key: "角色标识",
            keyHint: "小写字母开头，只含小写字母、数字、点、下划线与连字符，创建后不可修改。",
            keyLocked: "角色标识创建后不可修改。",
            keyRequired: "请输入角色标识",
            keyPattern: "需以小写字母开头，只含小写字母、数字、点、下划线与连字符，长度 2-64",
            name: "角色名",
            nameRequired: "请输入角色名",
            description: "说明",
            permissions: "权限",
            permissionsHint: "勾选即授予，取消勾选即收回；保存时按整份清单全量替换。",
        },
    },
    users: {
        assignRole: "设置角色",
        roleTitle: "设置「{{name}}」的后台角色",
        roleSubmit: "保存",
        roleHint: "保存后该用户的登录会话会被撤销，需要重新登录才能拿到新角色的权限。",
        roleLabel: "后台角色",
        roleNone: "不分配角色",
        roleNoneHint: "清空表示取消该用户的后台访问权限。",
        roleUpdated: "角色已更新",
    },
    // 多产品共用后台：产品清单在 admin/src/lib/products.ts 的注册表里，这里只放显示文案，
    // items 的键与注册表里的产品 key 一一对应。
    products: {
        planned: "规划中",
        integrationRequirement: "接入条件：后端实现同一套 /api/admin/* 契约并加入 CORS 白名单。",
        items: {
            "youc-canvas": { name: "优刻画布" },
            blog: { name: "博客" },
            office: { name: "办公套件" },
        },
        placeholder: {
            title: "「{{name}}」尚未接入统一后台",
            description: "该产品还没有接入统一管理后台，此处仅作占位说明，不提供任何管理功能。",
            requirementLabel: "接入条件",
            back: "返回当前产品",
        },
    },
};

// ===== translation 命名空间补丁：用量分析页与模型管理页 =====
// admin 页面文案按命名空间分层约定走 translation（见 admin/src/i18n/index.ts 的注释），
// 但那批键一直放在 web 语言包里，web 侧不在本应用的改动范围内；这里沿用 index.ts
// 补 admin.tabs.system 的同一做法（deep + 不覆盖），把缺失的键补进 translation，
// 侧边栏 labelKey（admin-layout 用默认命名空间渲染菜单）与页面文案才能解析。
// web 语言包以后补上同名键时会被保留，两边不会互相覆盖。
i18n.addResourceBundle(
    "zh-CN",
    "translation",
    {
        admin: {
            tabs: {
                analytics: "用量分析",
                membership: "会员管理",
                // 侧边栏「模型」下的能力子菜单文案。
                modelCapabilities: { image: "图片模型", video: "视频模型", audio: "音频模型", text: "文本模型" },
            },
            membership: {
                loadFailed: "订阅列表加载失败",
                filterUserPlaceholder: "按用户 ID 精确筛选",
                filterStatus: "订阅状态",
                grant: "发放 / 续期",
                compensate: "补偿发放",
                statuses: { active: "生效中", ended: "已结束" },
                columns: { user: "用户", plan: "档位", status: "状态", startedAt: "开始时间", periodEnd: "到期时间", sourceRef: "来源", createdAt: "创建时间", actions: "操作" },
                grantTitle: "发放 / 续期会员",
                compensateTitle: "补偿发放会员",
                user: "用户",
                userPlaceholder: "用户 ID 或邮箱",
                userHint: "服务端按用户 ID 或邮箱定位账号；用户不存在时会报错。",
                userRequired: "请输入用户 ID 或邮箱",
                plan: "会员档位",
                planRequired: "请选择会员档位",
                reason: "原因",
                reasonPlaceholder: "例如：客服补偿 / 活动赠送",
                reasonRequired: "必须填写原因",
                grantSubmit: "确认发放",
                granted: "会员已发放",
                compensated: "补偿已发放",
                revoke: "作废",
                revokeTitle: "作废「{{name}}」的会员订阅",
                revokeReason: "作废原因",
                revokeReasonRequired: "必须填写作废原因",
                revokeSubmit: "填写作废原因",
                revokeConfirmTitle: "确认作废该订阅？",
                revokeConfirmContent: "作废后该用户立即失去会员权益，操作不可恢复。",
                revokeOk: "确认作废",
                revoked: "订阅已作废",
            },
            analytics: {
                loadFailed: "用量分析数据加载失败",
                range: { d7: "近 7 天", d30: "近 30 天", d90: "近 90 天" },
                kpi: { requests: "生成请求", successRate: "成功率", cost: "消费点数", activeUsers: "活跃用户", activeSessions: "活跃会话" },
                trend: { title: "按天趋势", requests: "请求数", cost: "消费点数" },
                byModel: { title: "模型分布", requests: "请求数" },
                byCapability: { title: "能力分布" },
                bySpec: { title: "规格分布", requests: "请求数" },
                topUsers: { title: "消费用户排行", user: "用户", requests: "请求数", cost: "消费点数" },
            },
            models: {
                // 能力筛选 Segmented 与侧边栏「模型」能力子菜单的数量文案：选项文案 = 标签 + 数量。
                filters: { all: "全部", labeled: "{{label}} ({{count}})" },
                // 按能力分组的约束键里 web 语言包没有的键；size/quality/resolution/ratio/duration 沿用已有 constraintKeys。
                groups: { background: "背景 background", format: "格式 format", voice: "音色 voice", speed: "语速 speed" },
                // 视频能力的特性选项（web 语言包只有 referenceImage/mask）。
                features: { watermark: "水印", generateAudio: "生成音频", referenceVideo: "参考视频", referenceAudio: "参考音频" },
                nMaxImage: "单次张数（n.max）",
                nMaxText: "单次条数（n.max）",
                freeInputHint: "可选择建议值，也可输入自定义值",
                customConstraintsTitle: "自定义约束",
                customConstraintsHint: "不属于当前能力预置分组的约束键，取值原样保留；清空后保存即删除。",
            },
            // users 页的会员/存储加法列：档位沿用 admin.users.plans.*，这里只补新键。
            users: {
                membershipUntil: "至 {{date}}",
                membershipGrace: "宽限至 {{date}}",
                columns: { membership: "会员档位", storageQuota: "存储配额" },
            },
        },
    },
    true,
    false,
);
