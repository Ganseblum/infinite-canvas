import { Button, Result } from "antd";
import { useTranslation } from "react-i18next";
import { Navigate, useNavigate, useParams } from "react-router-dom";

import { getAdminProduct } from "@admin/lib/products";

// 规划中产品的占位页：只说明「尚未接入」与接入条件并提供返回入口，
// 不渲染任何假的管理功能菜单——管理动作只属于已接入的当前产品。
export default function AdminProductPlaceholderPage() {
    const { t } = useTranslation();
    const navigate = useNavigate();
    const { key } = useParams<{ key: string }>();
    const product = getAdminProduct(key);

    // 未登记的 key 或已接入产品都不该停在占位页：直接回当前产品后台。
    if (!product || product.status !== "planned") return <Navigate to="/admin" replace />;

    return (
        <Result
            status="info"
            title={t("products.placeholder.title", { ns: "admin", name: t(product.nameKey, { ns: "admin" }) })}
            subTitle={t("products.placeholder.description", { ns: "admin" })}
            extra={
                <Button type="primary" onClick={() => navigate("/admin")}>
                    {t("products.placeholder.back", { ns: "admin" })}
                </Button>
            }
        >
            <div className="mx-auto max-w-md rounded-lg border border-stone-200 p-4 text-left text-sm dark:border-stone-800">
                <p className="font-medium">{t("products.placeholder.requirementLabel", { ns: "admin" })}</p>
                <p className="mt-1 text-stone-500 dark:text-stone-400">{t(product.noteKey ?? "products.integrationRequirement", { ns: "admin" })}</p>
            </div>
        </Result>
    );
}
