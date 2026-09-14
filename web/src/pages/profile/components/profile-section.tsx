import { useEffect } from "react";
import { App, Button, Form, Input, Tag } from "antd";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import type { MeUser } from "@/services/api/account";
import { updateProfile } from "@/services/api/account";

type ProfileFormValues = {
    displayName: string;
    avatarUrl?: string;
};

export function ProfileSection({ user }: { user: MeUser }) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [form] = Form.useForm<ProfileFormValues>();

    useEffect(() => {
        form.setFieldsValue({ displayName: user.displayName || "", avatarUrl: user.avatarUrl || "" });
    }, [form, user.displayName, user.avatarUrl]);

    const mutation = useMutation({
        mutationFn: (values: ProfileFormValues) => updateProfile({ displayName: values.displayName.trim(), avatarUrl: values.avatarUrl?.trim() ?? "" }),
        onSuccess: async () => {
            message.success(t("profile.basics.saved"));
            await queryClient.invalidateQueries({ queryKey: ["me", user.id] });
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    return (
        <section id="profile-basics" className="scroll-mt-4 rounded-xl border border-stone-200 p-6 dark:border-stone-800">
            <h2 className="text-lg font-semibold">{t("profile.basics.title")}</h2>
            <Form form={form} layout="vertical" className="mt-5" onFinish={(values) => mutation.mutate(values)}>
                <div className="grid gap-4 sm:grid-cols-2">
                    <Form.Item name="displayName" label={t("profile.basics.displayName")} rules={[{ required: true, message: t("profile.basics.displayNameRequired") }, { max: 64, message: t("profile.basics.displayNameMax") }]}>
                        <Input maxLength={64} />
                    </Form.Item>
                    <Form.Item label={t("profile.basics.email")}>
                        <div className="flex min-h-8 items-center gap-2">
                            <span className="truncate text-sm text-stone-600 dark:text-stone-300">{user.email}</span>
                            <Tag color={user.emailVerified ? "success" : "warning"} className="m-0 shrink-0">
                                {user.emailVerified ? t("profile.basics.verified") : t("profile.basics.unverified")}
                            </Tag>
                        </div>
                        {user.emailVerified ? null : <p className="mt-1 text-xs text-stone-500 dark:text-stone-400">{t("profile.basics.unverifiedHint")}</p>}
                    </Form.Item>
                    <Form.Item name="avatarUrl" label={t("profile.basics.avatarUrl")} className="sm:col-span-2">
                        <Input placeholder={t("profile.basics.avatarUrlPlaceholder")} />
                    </Form.Item>
                </div>
                <div className="flex justify-end gap-3 border-t border-stone-200 pt-5 dark:border-stone-800">
                    <Button onClick={() => form.setFieldsValue({ displayName: user.displayName || "", avatarUrl: user.avatarUrl || "" })}>{t("profile.basics.reset")}</Button>
                    <Button type="primary" htmlType="submit" loading={mutation.isPending}>
                        {t("profile.basics.save")}
                    </Button>
                </div>
            </Form>
        </section>
    );
}
