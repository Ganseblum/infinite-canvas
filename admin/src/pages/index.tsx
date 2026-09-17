import { Skeleton, Card, Col, Row, Statistic } from "antd";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { formatMoney, formatPoints } from "@/lib/credits-format";
import { formatBytes } from "@/lib/image-utils";
import { QueryError } from "@admin/components/query-error";
import { getAdminStats } from "@admin/services/api/admin";
import { RevenueCards } from "./system";

export default function AdminDashboardPage() {
    const { t } = useTranslation();
    const statsQuery = useQuery({
        queryKey: ["admin", "stats"],
        queryFn: ({ signal }) => getAdminStats(signal),
        refetchOnWindowFocus: true,
    });

    if (statsQuery.isError) {
        return <QueryError error={statsQuery.error} message={t("admin.stats.loadFailed")} onRetry={() => void statsQuery.refetch()} />;
    }

    if (statsQuery.isPending || !statsQuery.data) {
        return <Skeleton active paragraph={{ rows: 6 }} />;
    }

    const stats = statsQuery.data;
    const cards = [
        { key: "users", value: stats.userTotal, suffix: t("admin.stats.userSuffix", { count: stats.userToday }) },
        { key: "storage", value: formatBytes(stats.storageBytes) },
        { key: "generations", value: stats.generationToday },
        { key: "orders", value: stats.orderToday, suffix: formatMoney(stats.revenueMicrosToday) },
        { key: "credits", value: formatPoints(stats.creditsTotal) },
    ] as const;

    return (
        <div className="flex flex-col gap-4">
            <Row gutter={[16, 16]}>
                {cards.map((card) => (
                    <Col key={card.key} xs={24} sm={12} lg={8}>
                        <Card size="small">
                            <Statistic title={t(`admin.stats.${card.key}`)} value={card.value} suffix={"suffix" in card ? card.suffix : undefined} />
                        </Card>
                    </Col>
                ))}
            </Row>
            <RevenueCards />
        </div>
    );
}
