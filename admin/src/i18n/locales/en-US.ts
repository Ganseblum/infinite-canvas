import i18n from "@/i18n";

export default {
    docTitle: "Admin · YOUC",
    previewFailed: "Failed to load the quarantined original. Refresh and try again.",
    currentRole: "Role: {{role}}",
    login: {
        title: "Administrator sign-in",
        description: "Sign in with an administrator account to open the admin console.",
        account: "Account",
        accountPlaceholder: "Email or username",
        password: "Password",
        passwordPlaceholder: "Enter your password",
        accountRequired: "Enter your account",
        passwordRequired: "Enter your password",
        submit: "Sign in",
    },
    // The session is shared across apps: the refresh cookie is host-only on the API origin
    // (sim-art.youc.online, path /api/auth) and both frontends call the same API origin.
    sharedSession: {
        loginNote: "The session is shared with the main site: signing in here also signs you in there.",
        logoutNote: "The session is shared with the main site: signing out here also signs you out there.",
    },
    // Environment badge: production always shows a prominent red badge; test/dev reuse the amber style.
    envBadge: {
        production: "PRODUCTION",
        productionTitle: "This is the production environment. Proceed carefully.",
        test: "TEST",
        dev: "DEV",
        nonProductionTitle: "This is not the production environment",
    },
    forbidden: {
        title: "This account has no console access",
        description: "The current account {{email}} can use the main site as usual, but it has no access to the admin console. Ask an administrator to grant access, or sign in with an administrator account.",
    },
    // Forced password change: while the flag is set the server only allows sign-out, refresh and the
    // password change itself. This page is the only landing spot; changing the password clears the
    // flag and enters the console automatically, no re-sign-in needed.
    changePassword: {
        title: "Password change required",
        description: "For account security, this account must change its password before using the admin console. You will enter the console automatically after the change; no need to sign in again.",
        oldPassword: "Current password",
        oldPasswordPlaceholder: "Enter the current password",
        oldPasswordRequired: "Enter the current password",
        oldPasswordWrong: "The current password is incorrect",
        newPassword: "New password",
        newPasswordPlaceholder: "Enter the new password",
        newPasswordRequired: "Enter the new password",
        newPasswordLength: "The new password must be 8-72 characters long",
        confirmPassword: "Confirm new password",
        confirmPasswordPlaceholder: "Enter the new password again",
        confirmPasswordRequired: "Enter the new password again",
        confirmPasswordMismatch: "The two passwords do not match",
        submit: "Change password and continue",
    },
    noPermission: {
        title: "This role cannot open this page",
        description: "The role \"{{role}}\" does not include the permission this page requires. The sidebar only lists pages you can open; ask a system administrator to update the role if you need more access.",
        descriptionNoRole: "This account has no console role yet. The sidebar only lists pages you can open; ask a system administrator for access.",
        back: "Go to an allowed page",
    },
    permissionDenied: {
        title: "Permission denied",
        description: "This role cannot call this API, and retrying will not change the result. Menus and entries are hidden by permission; ask a system administrator to update the role if you need more access.",
    },
    roles: {
        tab: "Roles & members",
        hint: "A role defines what the console shows and allows. Permission keys come from the server-side registry; the UI only assigns them to roles and never adds or removes permissions.",
        readOnlyHint: "Your role is read-only here.",
        create: "New role",
        createTitle: "New role",
        editTitle: "Edit role: {{name}}",
        created: "Role created",
        updated: "Role saved",
        deleted: "Role deleted",
        system: "System role",
        custom: "Custom role",
        allPermissions: "All permissions",
        view: "View",
        edit: "Edit",
        delete: "Delete",
        deleteConfirm: "Delete the role \"{{name}}\"? This cannot be undone.",
        systemNoDelete: "The system role cannot be deleted",
        hasMembers: "This role still has members; reassign them first",
        systemNoEdit: "The system role implicitly holds every permission, so its permissions cannot be edited; only the display name and description can change.",
        membersTitle: "System role members",
        columns: {
            role: "Role",
            description: "Description",
            type: "Type",
            permissions: "Permissions",
            members: "Members",
            actions: "Actions",
        },
        fields: {
            key: "Role key",
            keyHint: "Starts with a lowercase letter; lowercase letters, digits, dots, underscores and hyphens only; cannot be changed later.",
            keyLocked: "The role key cannot be changed after creation.",
            keyRequired: "Enter the role key",
            keyPattern: "Start with a lowercase letter; lowercase letters, digits, dots, underscores and hyphens only; 2-64 characters",
            name: "Role name",
            nameRequired: "Enter the role name",
            description: "Description",
            permissions: "Permissions",
            permissionsHint: "Check to grant, uncheck to revoke; saving replaces the whole list.",
        },
    },
    users: {
        assignRole: "Set role",
        roleTitle: "Set the console role of \"{{name}}\"",
        roleSubmit: "Save",
        roleHint: "Saving revokes this user's sessions: they must sign in again to receive the new role's permissions.",
        roleLabel: "Console role",
        roleNone: "No role",
        roleNoneHint: "Clearing this removes the user's console access.",
        roleUpdated: "Role updated",
    },
    // Multi-product console: the product list lives in the registry at admin/src/lib/products.ts;
    // this section only holds display copy, keyed to match the registry's product keys.
    products: {
        planned: "Planned",
        integrationRequirement: "Integration requirement: the backend must implement the same /api/admin/* contract and be added to the CORS allowlist.",
        items: {
            "youc-canvas": { name: "YOUC Canvas" },
            blog: { name: "Blog" },
            office: { name: "Office suite" },
        },
        placeholder: {
            title: "\"{{name}}\" is not connected to the unified console yet",
            description: "This product has not been connected to the unified admin console. This page is a placeholder and offers no management features.",
            requirementLabel: "Integration requirement",
            back: "Back to the current product",
        },
    },
};

// ===== Translation namespace patch: usage analytics and the models page =====
// Admin page copy lives in the translation namespace by convention (see the comment in
// admin/src/i18n/index.ts), but those keys ship in the web language packs, which are outside
// this app's change scope. Reuse the same approach index.ts applies to admin.tabs.system
// (deep merge, never overwrite) to fill the missing keys, so the sidebar
// labelKey (rendered by admin-layout through the default namespace) and page copy resolve.
// If the web packs gain the same keys later, those win and this patch stays harmless.
i18n.addResourceBundle(
    "en-US",
    "translation",
    {
        admin: {
            tabs: {
                analytics: "Usage analytics",
                // Capability shortcuts under the sidebar "Models" submenu.
                modelCapabilities: { image: "Image models", video: "Video models", audio: "Audio models", text: "Text models" },
            },
            analytics: {
                loadFailed: "Failed to load usage analytics",
                range: { d7: "Last 7 days", d30: "Last 30 days", d90: "Last 90 days" },
                kpi: { requests: "Generation requests", successRate: "Success rate", cost: "Credits spent", activeUsers: "Active users", activeSessions: "Active sessions" },
                trend: { title: "Daily trend", requests: "Requests", cost: "Credits spent" },
                byModel: { title: "By model", requests: "Requests" },
                byCapability: { title: "By capability" },
                bySpec: { title: "By spec", requests: "Requests" },
                topUsers: { title: "Top consumers", user: "User", requests: "Requests", cost: "Credits spent" },
            },
            models: {
                // Count labels shared by the models page Segmented and the sidebar capability shortcuts: label + count.
                filters: { all: "All", labeled: "{{label}} ({{count}})" },
                // Constraint keys used by the capability groups that the web packs lack;
                // size/quality/resolution/ratio/duration reuse the existing constraintKeys.
                groups: { background: "Background (background)", format: "Format (format)", voice: "Voice (voice)", speed: "Speed (speed)" },
                // Video capability feature options (the web packs only ship referenceImage/mask).
                features: { watermark: "Watermark", generateAudio: "Generate audio", referenceVideo: "Reference video", referenceAudio: "Reference audio" },
                nMaxImage: "Images per request (n.max)",
                nMaxText: "Items per request (n.max)",
                freeInputHint: "Pick a suggested value or type a custom one",
                customConstraintsTitle: "Custom constraints",
                customConstraintsHint: "Constraint keys outside this capability's preset groups are kept as-is; clearing the values removes the key on save.",
            },
        },
    },
    true,
    false,
);
