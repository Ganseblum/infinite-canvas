import { useState } from "react";
import { Alert, App, Button, Card, Col, Descriptions, Drawer, Form, Input, InputNumber, Modal, Row, Select, Space, Statistic, Table, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { formatPoints } from "@/lib/credits-format";
import {
    compensateModerationRecord,
    getModerationRecord,
    getModerationStats,
    listModerationRecords,
    reviewModerationRecord,
    type ModerationRecord,
} from "@/services/api/admin";

const DECISION_COLORS: Record<string, string> = { passed: "success", rejected: "error", error: "warning", pending: "processing" };
const REVIEW_COLORS: Record<string, string> = { not_required: "default", pending: "warning", approved: "success", rejected: "error" };
const STAGES = ["prompt", "reference", "artifact", "upload"] as const;
const DECISIONS = ["passed", "rejected", "error"] as const;
const REVIEW_STATUSES = ["not_required", "pending", "approved", "rejected"] as const;

type CompensateForm = { amountMicros: number; note: string };
type ReviewForm = { decision: "approved" | "rejected"; note: string };

export default function AdminModerationPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [page, setPage] = useState(1);
    const [size, setSize] = useState(20);
    const [stage, setStage] = useState<string | undefined>();
    const [decision, setDecision] = useState<string | undefined>();
    const [reviewStatus, setReviewStatus] = useState<string | undefined>("pending");
    const [label, setLabel] = useState<string | undefined>();
    const [selectedId, setSelectedId] = useState<string | null>(null);
    const [reviewTarget, setReviewTarget] = useState<ModerationRecord | null>(null);
    const [compensateTarget, setCompensateTarget] = useState<ModerationRecord | null>(null);
    const [reviewForm] = Form.useForm<ReviewForm>();
    const [compensateForm] = Form.useForm<CompensateForm>();

    const statsQuery = useQuery({
        queryKey: ["admin", "moderation", "stats"],
        queryFn: ({ signal }) => getModerationStats(signal),
        refetchOnWindowFocus: true,
    });
    const recordsQuery = useQuery({
        queryKey: ["admin", "moderation", "records", page, size, stage, decision, reviewStatus, label],
        queryFn: ({ signal }) => listModerationRecords({ page, size, stage, decision, reviewStatus, label, sort: "-createdAt" }, signal),
        placeholderData: (previous) => previous,
    });
    const detailQuery = useQuery({
        queryKey: ["admin", "moderation", "record", selectedId],
        queryFn: ({ signal }) => getModerationRecord(selectedId as string, signal),
        enabled: !!selectedId,
    });

    const invalidate = async () => {
        await queryClient.invalidateQueries({ queryKey: ["admin", "moderation"] });
    };

    const reviewMutation = useMutation({
        mutationFn: ({ id, values }: { id: string; values: ReviewForm }) =>
            reviewModerationRecord(id, { ...values, revision: detailQuery.data?.record.reviewRevision ?? reviewTarget?.reviewRevision ?? 0 }),
        onSuccess: async (result) => {
            if (result.released) message.success(t("admin.moderation.reviewReleased"));
            else if (result.releaseError) message.warning(result.releaseError);
            else message.success(t("admin.moderation.reviewed"));
            setReviewTarget(null);
            reviewForm.resetFields();
            await invalidate();
        },
        onError: async (error) => {
            message.error(getApiErrorMessage(error));
            await invalidate();
        },
    });

    const compensateMutation = useMutation({
        mutationFn: ({ id, values }: { id: string; values: CompensateForm }) => compensateModerationRecord(id, values),
        onSuccess: async () => {
            message.success(t("admin.moderation.compensated"));
            setCompensateTarget(null);
            compensateForm.resetFields();
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const columns: ColumnsType<ModerationRecord> = [
        {
            title: t("admin.moderation.columns.record"),
            dataIndex: "contentHash",
            render: (value: string, row) => (
                <div className="min-w-0">
                    <div className="font-mono text-xs">{value.slice(0, 12)}…</div>
                    <div className="truncate text-xs text-stone-500 dark:text-stone-400">{dayjs(row.createdAt).format("YYYY-MM-DD HH:mm")}</div>
                </div>
            ),
        },
        { title: t("admin.moderation.columns.stage"), dataIndex: "stage", width: 100, render: (value: string) => t(`admin.moderation.stages.${value}`) },
        {
            title: t("admin.moderation.columns.decision"),
            dataIndex: "decision",
            width: 110,
            render: (value: string) => <Tag color={DECISION_COLORS[value]}>{t(`admin.moderation.decisions.${value}`)}</Tag>,
        },
        {
            title: t("admin.moderation.columns.labels"),
            dataIndex: "riskLabels",
            width: 160,
            render: (values: string[]) => (values?.length ? values.map((item) => <Tag key={item}>{item}</Tag>) : "—"),
        },
        {
            title: t("admin.moderation.columns.review"),
            dataIndex: "reviewStatus",
            width: 120,
            render: (value: string) => <Tag color={REVIEW_COLORS[value]}>{t(`admin.moderation.reviewStatuses.${value}`)}</Tag>,
        },
        {
            title: t("admin.moderation.columns.compensated"),
            dataIndex: "compensatedMicros",
            width: 110,
            align: "right",
            render: (value: number) => (value > 0 ? formatPoints(value) : "—"),
        },
        {
            title: t("admin.moderation.columns.actions"),
            key: "actions",
            width: 220,
            render: (_, row) => (
                <Space size={2} wrap>
                    <Button size="small" type="link" className="!px-1" onClick={() => setSelectedId(row.id)}>
                        {t("admin.moderation.detailAction")}
                    </Button>
                    {row.reviewStatus === "pending" ? (
                        <Button
                            size="small"
                            type="link"
                            className="!px-1"
                            onClick={() => {
                                setReviewTarget(row);
                                reviewForm.setFieldsValue({ decision: "approved", note: "" });
                            }}
                        >
                            {t("admin.moderation.review")}
                        </Button>
                    ) : null}
                    {row.reviewStatus === "approved" && row.compensatedMicros === 0 ? (
                        <Button
                            size="small"
                            type="link"
                            className="!px-1"
                            onClick={() => {
                                setCompensateTarget(row);
                                compensateForm.setFieldsValue({ amountMicros: 100000, note: "" });
                            }}
                        >
                            {t("admin.moderation.compensate")}
                        </Button>
                    ) : null}
                </Space>
            ),
        },
    ];

    const stats = statsQuery.data;

    return (
        <div className="flex flex-col gap-4">
            {stats ? (
                <Row gutter={[16, 16]}>
                    {[
                        { key: "total", value: stats.total },
                        { key: "rejectedRate", value: `${(stats.rejectedRate * 100).toFixed(1)}%` },
                        { key: "errorRate", value: `${(stats.errorRate * 100).toFixed(1)}%` },
                        { key: "pendingReview", value: stats.pendingReview },
                        { key: "reviewRate", value: `${(stats.reviewRate * 100).toFixed(1)}%` },
                        { key: "quarantineCount", value: stats.quarantineCount },
                    ].map((card) => (
                        <Col key={card.key} xs={12} sm={8} lg={4}>
                            <Card size="small">
                                <Statistic title={t(`admin.moderation.stats.${card.key}`)} value={card.value} />
                            </Card>
                        </Col>
                    ))}
                </Row>
            ) : null}

            <div className="flex flex-wrap items-center gap-3">
                <Select
                    allowClear
                    className="w-32"
                    placeholder={t("admin.moderation.filterStage")}
                    value={stage}
                    onChange={(value) => {
                        setStage(value);
                        setPage(1);
                    }}
                    options={STAGES.map((value) => ({ value, label: t(`admin.moderation.stages.${value}`) }))}
                />
                <Select
                    allowClear
                    className="w-32"
                    placeholder={t("admin.moderation.filterDecision")}
                    value={decision}
                    onChange={(value) => {
                        setDecision(value);
                        setPage(1);
                    }}
                    options={DECISIONS.map((value) => ({ value, label: t(`admin.moderation.decisions.${value}`) }))}
                />
                <Select
                    allowClear
                    className="w-36"
                    placeholder={t("admin.moderation.filterReview")}
                    value={reviewStatus}
                    onChange={(value) => {
                        setReviewStatus(value);
                        setPage(1);
                    }}
                    options={REVIEW_STATUSES.map((value) => ({ value, label: t(`admin.moderation.reviewStatuses.${value}`) }))}
                />
                <Input.Search
                    allowClear
                    className="w-48"
                    placeholder={t("admin.moderation.filterLabel")}
                    onSearch={(value) => {
                        setLabel(value.trim() || undefined);
                        setPage(1);
                    }}
                />
            </div>

            {recordsQuery.isError ? (
                <Alert
                    type="error"
                    showIcon
                    message={t("admin.moderation.loadFailed")}
                    description={getApiErrorMessage(recordsQuery.error)}
                    action={<Button size="small" onClick={() => void recordsQuery.refetch()}>{t("admin.retry")}</Button>}
                />
            ) : (
                <Table<ModerationRecord>
                    rowKey="id"
                    size="middle"
                    loading={recordsQuery.isPending}
                    columns={columns}
                    dataSource={recordsQuery.data?.items ?? []}
                    scroll={{ x: 1000 }}
                    pagination={{
                        current: page,
                        pageSize: size,
                        total: recordsQuery.data?.total ?? 0,
                        showSizeChanger: true,
                        onChange: (nextPage, nextSize) => {
                            setPage(nextPage);
                            setSize(nextSize);
                        },
                    }}
                />
            )}

            <Drawer open={!!selectedId} width={640} title={t("admin.moderation.detailTitle")} onClose={() => setSelectedId(null)}>
                {detailQuery.isError ? (
                    <Alert type="error" showIcon message={t("admin.moderation.detailFailed")} description={getApiErrorMessage(detailQuery.error)} />
                ) : detailQuery.isPending || !detailQuery.data ? (
                    <div className="py-6 text-center text-sm text-stone-500 dark:text-stone-400">{t("admin.loading")}</div>
                ) : (
                    <div className="flex flex-col gap-4">
                        <Descriptions size="small" column={1} bordered>
                            <Descriptions.Item label={t("admin.moderation.detail.stage")}>{t(`admin.moderation.stages.${detailQuery.data.record.stage}`)}</Descriptions.Item>
                            <Descriptions.Item label={t("admin.moderation.detail.hash")}>
                                <span className="font-mono text-xs">{detailQuery.data.record.contentHash}</span>
                            </Descriptions.Item>
                            <Descriptions.Item label={t("admin.moderation.detail.provider")}>
                                {detailQuery.data.record.provider} · {detailQuery.data.record.providerRequestId || "—"}
                            </Descriptions.Item>
                            <Descriptions.Item label={t("admin.moderation.detail.policy")}>{detailQuery.data.record.policyVersion}</Descriptions.Item>
                            <Descriptions.Item label={t("admin.moderation.detail.decision")}>
                                <Tag color={DECISION_COLORS[detailQuery.data.record.decision]}>{t(`admin.moderation.decisions.${detailQuery.data.record.decision}`)}</Tag>
                            </Descriptions.Item>
                            <Descriptions.Item label={t("admin.moderation.detail.labels")}>
                                {(detailQuery.data.record.riskLabels ?? []).map((item) => (
                                    <Tag key={item}>{item}</Tag>
                                ))}
                            </Descriptions.Item>
                            <Descriptions.Item label={t("admin.moderation.detail.summary")}>
                                <pre className="max-h-40 overflow-auto text-xs">{JSON.stringify(detailQuery.data.record.providerResult ?? {}, null, 2)}</pre>
                            </Descriptions.Item>
                        </Descriptions>
                        {detailQuery.data.record.quarantine?.available ? (
                            <div className="flex flex-col gap-2">
                                <p className="text-sm text-stone-500 dark:text-stone-400">{t("admin.moderation.previewHint")}</p>
                                <img
                                    src={detailQuery.data.record.quarantine.previewPath}
                                    alt={t("admin.moderation.previewAlt")}
                                    className="max-h-72 w-full rounded-lg border border-stone-200 object-contain dark:border-stone-800"
                                />
                            </div>
                        ) : (
                            <Alert type="info" showIcon message={detailQuery.data.record.quarantine?.reason || t("admin.moderation.noPreview")} />
                        )}
                        <div className="flex flex-wrap gap-2">
                            <Button
                                disabled={detailQuery.data.record.reviewStatus !== "pending"}
                                onClick={() => {
                                    setReviewTarget(detailQuery.data!.record);
                                    reviewForm.setFieldsValue({ decision: "approved", note: "" });
                                }}
                            >
                                {t("admin.moderation.review")}
                            </Button>
                            <Button
                                disabled={detailQuery.data.record.reviewStatus !== "approved" || detailQuery.data.record.compensatedMicros > 0}
                                onClick={() => {
                                    setCompensateTarget(detailQuery.data!.record);
                                    compensateForm.setFieldsValue({ amountMicros: 100000, note: "" });
                                }}
                            >
                                {t("admin.moderation.compensate")}
                            </Button>
                        </div>
                    </div>
                )}
            </Drawer>

            <Modal
                open={!!reviewTarget}
                title={t("admin.moderation.reviewTitle")}
                okText={t("admin.moderation.reviewSubmit")}
                cancelText={t("common.cancel")}
                confirmLoading={reviewMutation.isPending}
                onCancel={() => setReviewTarget(null)}
                onOk={async () => {
                    const values = await reviewForm.validateFields();
                    if (reviewTarget) await reviewMutation.mutateAsync({ id: reviewTarget.id, values });
                }}
            >
                <Alert className="mb-4" type="info" showIcon message={t("admin.moderation.reviewHint")} />
                <Form form={reviewForm} layout="vertical">
                    <Form.Item name="decision" label={t("admin.moderation.detail.decision")} rules={[{ required: true }]}>
                        <Select
                            options={[
                                { value: "approved", label: t("admin.moderation.reviewStatuses.approved") },
                                { value: "rejected", label: t("admin.moderation.reviewStatuses.rejected") },
                            ]}
                        />
                    </Form.Item>
                    <Form.Item name="note" label={t("admin.moderation.note")} rules={[{ required: true, message: t("admin.moderation.noteRequired") }]}>
                        <Input maxLength={200} />
                    </Form.Item>
                </Form>
            </Modal>

            <Modal
                open={!!compensateTarget}
                title={t("admin.moderation.compensateTitle")}
                okText={t("admin.moderation.compensateSubmit")}
                cancelText={t("common.cancel")}
                confirmLoading={compensateMutation.isPending}
                onCancel={() => setCompensateTarget(null)}
                onOk={async () => {
                    const values = await compensateForm.validateFields();
                    if (compensateTarget) await compensateMutation.mutateAsync({ id: compensateTarget.id, values });
                }}
            >
                <Alert className="mb-4" type="warning" showIcon message={t("admin.moderation.compensateHint")} />
                <Form form={compensateForm} layout="vertical">
                    <Form.Item name="amountMicros" label={t("admin.moderation.amount")} rules={[{ required: true }]}>
                        <InputNumber className="w-full" step={100000} precision={0} min={1} />
                    </Form.Item>
                    <Form.Item name="note" label={t("admin.moderation.note")} rules={[{ required: true, message: t("admin.moderation.noteRequired") }]}>
                        <Input maxLength={200} />
                    </Form.Item>
                </Form>
            </Modal>
        </div>
    );
}
