import { Alert, Button, Modal, Typography } from "antd";
import { useTranslation } from "react-i18next";

// 一次性明文 secret 展示：新建与重置密钥共用。接口只在这里返回一次明文，
// 关闭后无法再查看，只能重置生成新密钥；复制走 Typography 自带的复制按钮与提示。
export function SecretModal({ view, onClose }: { view: { clientId: string; secret: string } | null; onClose: () => void }) {
    const { t } = useTranslation();
    const tooltips = [t("common.copy"), t("common.copied")];

    return (
        <Modal
            open={!!view}
            title={t("admin.sso.secret.title")}
            width={560}
            footer={
                <Button type="primary" onClick={onClose}>
                    {t("common.done")}
                </Button>
            }
            onCancel={onClose}
            maskClosable={false}
        >
            <Alert className="mb-4" type="warning" showIcon message={t("admin.sso.secret.warning")} />
            {view ? (
                <div className="flex flex-col gap-4">
                    <div>
                        <div className="mb-1 text-sm text-stone-500 dark:text-stone-400">{t("admin.sso.secret.clientLabel")}</div>
                        <Typography.Text className="font-mono text-xs" copyable={{ tooltips }}>
                            {view.clientId}
                        </Typography.Text>
                    </div>
                    <div>
                        <div className="mb-1 text-sm text-stone-500 dark:text-stone-400">{t("admin.sso.secret.secretLabel")}</div>
                        <Typography.Paragraph className="!mb-0 font-mono text-xs" copyable={{ text: view.secret, tooltips }}>
                            {view.secret}
                        </Typography.Paragraph>
                    </div>
                </div>
            ) : null}
        </Modal>
    );
}
