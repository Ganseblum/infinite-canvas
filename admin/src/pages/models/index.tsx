import { useMemo, useState } from "react";
import { Alert, App, Button, Descriptions, Divider, Drawer, Form, Input, InputNumber, Modal, Popconfirm, Segmented, Select, Space, Switch, Table, Tag } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import dayjs from "dayjs";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { formatMoney, formatPoints } from "@/lib/credits-format";
import { QueryError } from "@admin/components/query-error";
import {
    createAdminModel,
    listAdminChannels,
    createAdminPromotion,
    deleteAdminModel,
    listAdminModels,
    listAdminPromotions,
    updateAdminModel,
    updateAdminPromotion,
    type AdminModel,
    type AdminPromotion,
} from "@admin/services/api/admin";
import type { ModelCapability, ModelConstraints, ModelCreditCost } from "@/services/api/catalog";

const CAPABILITIES: ModelCapability[] = ["image", "video", "text", "audio"];
// 旧版通用约束键清单。约束表单已改为按能力分组（见 CAPABILITY_GROUPS），这份清单现在只剩
// ConstraintKey 类型与折扣活动的匹配参数下拉两个用途：服务端 ValidatePromotion /
// ValidateCreditCost 都只接受这五个键，所以匹配参数与计价维度都不随新分组扩大。
const CONSTRAINT_KEYS = ["size", "ratio", "resolution", "quality", "duration"] as const;

// 每个能力的约束分组：key 与 constraints 里的键一一对应，数据结构与提交格式完全不变，只是按能力分区展示。
// tags = 建议值之外允许自由输入；multiple = 固定集合，只能从建议值里选。
type CapabilityGroup = { key: string; labelKey: string; options?: string[]; tags?: boolean };

const CAPABILITY_GROUPS: Record<ModelCapability, CapabilityGroup[]> = {
    image: [
        { key: "size", labelKey: "admin.models.constraintKeys.size", options: ["1024x1024", "1536x1024", "1024x1536", "2048x2048"], tags: true },
        { key: "quality", labelKey: "admin.models.constraintKeys.quality", options: ["low", "high"] },
        { key: "background", labelKey: "admin.models.groups.background", options: ["opaque", "transparent"] },
    ],
    video: [
        { key: "resolution", labelKey: "admin.models.constraintKeys.resolution", options: ["480p", "720p", "1080p"] },
        { key: "ratio", labelKey: "admin.models.constraintKeys.ratio", options: ["1:1", "16:9", "9:16", "3:4", "4:3", "21:9"] },
        { key: "duration", labelKey: "admin.models.constraintKeys.duration", options: ["4", "6", "8", "12", "16", "24", "30"], tags: true },
    ],
    audio: [
        { key: "format", labelKey: "admin.models.groups.format", options: ["mp3", "wav", "opus"] },
        { key: "voice", labelKey: "admin.models.groups.voice", tags: true },
        { key: "speed", labelKey: "admin.models.groups.speed", tags: true },
    ],
    text: [],
};

// 特性（constraints.features）的可选项也按能力区分；空数组表示该能力不展示特性控件。
const CAPABILITY_FEATURES: Record<ModelCapability, string[]> = {
    image: ["referenceImage", "mask"],
    video: ["watermark", "generateAudio", "referenceVideo", "referenceAudio"],
    audio: [],
    text: [],
};

type ConstraintKey = (typeof CONSTRAINT_KEYS)[number];

type ModelFormValues = {
    channelIds?: string[];
    name: string;
    displayName: string;
    capability: ModelCapability;
    provider: string;
    sort: number;
    enabled: boolean;
    freeTrialEligible: boolean;
    constraints: Record<string, string[]>;
    nMax?: number;
    features: string[];
    dimensions: ConstraintKey[];
    prices: Record<string, number>;
};

function microsToYuan(micros: number) {
    return Number((micros / 1_000_000).toFixed(4));
}

function yuanToMicros(yuan: number) {
    return Math.round(yuan * 1_000_000);
}

function combinationKey(parts: string[]) {
    return parts.join("\u0000");
}

// 约束取值可能是字符串、数字或 { value, label } 对象，统一成字符串再参与组合与价格键。
function optionValue(option: ModelConstraints[keyof ModelConstraints] extends Array<infer T> ? T : never): string {
    if (option !== null && typeof option === "object") return String((option as { value: string | number }).value);
    return String(option);
}

function buildCombinations(dimensions: ConstraintKey[], constraints: Record<string, string[]>) {
    const lists = dimensions.map((dimension) => constraints[dimension] ?? []).filter((list) => list.length > 0);
    if (lists.length !== dimensions.length) return [];
    return lists.reduce<string[][]>((acc, list) => acc.flatMap((prefix) => list.map((value) => [...prefix, value])), [[]]);
}

export default function AdminModelsPage() {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [editing, setEditing] = useState<AdminModel | null>(null);
    const [creating, setCreating] = useState(false);
    const [promotionModel, setPromotionModel] = useState<AdminModel | null>(null);
    const [form] = Form.useForm<ModelFormValues>();
    const dimensions = Form.useWatch("dimensions", form) ?? [];
    const constraintsWatch = Form.useWatch("constraints", form) ?? {};
    const featuresWatch = Form.useWatch("features", form) ?? [];
    const capabilityWatch = Form.useWatch("capability", form);

    // 当前能力对应的约束分组与控件开关：能力切换时保留已填值，仅切换可见分组。
    const activeCapability: ModelCapability = capabilityWatch ?? "image";
    const activeGroups = CAPABILITY_GROUPS[activeCapability];
    const featureOptions = CAPABILITY_FEATURES[activeCapability];
    const showNMax = activeCapability === "image" || activeCapability === "text";
    const showFeatures = featureOptions.length > 0;
    // 不属于当前能力预置分组的约束键（旧数据自定义键、换能力后遗留的键）收进「自定义约束」，取值原样保留。
    const customConstraintKeys = useMemo(() => {
        const known = new Set(activeGroups.map((group) => group.key));
        return Object.entries(constraintsWatch)
            .filter(([key, list]) => Array.isArray(list) && list.length > 0 && !known.has(key))
            .map(([key]) => key)
            .sort();
    }, [activeGroups, constraintsWatch]);

    const modelsQuery = useQuery({
        queryKey: ["admin", "models"],
        queryFn: ({ signal }) => listAdminModels(signal),
    });
    const channelsQuery = useQuery({
        queryKey: ["admin", "channels"],
        queryFn: ({ signal }) => listAdminChannels(signal),
    });

    // 表格上方的能力筛选：全部/图片/视频/音频/文本，选项上带各能力的模型数量。
    const [capabilityFilter, setCapabilityFilter] = useState<"all" | ModelCapability>("all");
    const allModels = modelsQuery.data?.items ?? [];
    const capabilityCounts = useMemo(() => {
        const counts: Record<"all" | ModelCapability, number> = { all: allModels.length, image: 0, video: 0, text: 0, audio: 0 };
        for (const item of allModels) counts[item.capability] += 1;
        return counts;
    }, [allModels]);
    const filteredModels = capabilityFilter === "all" ? allModels : allModels.filter((item) => item.capability === capabilityFilter);

    const invalidate = async () => {
        await Promise.all([
            queryClient.invalidateQueries({ queryKey: ["admin", "models"] }),
            queryClient.invalidateQueries({ queryKey: ["models"] }),
        ]);
    };

    const saveMutation = useMutation({
        mutationFn: (values: ModelFormValues) => {
            // 提交格式不变：constraints 仍是键到字符串数组的映射。按能力分组后表单里可能出现
            // 预置分组之外的键（旧数据自定义键），只要取值非空就原样提交。
            const constraints = {} as ModelConstraints;
            const constraintRecord = constraints as Record<string, string[]>;
            for (const [key, list] of Object.entries(values.constraints ?? {})) {
                if (Array.isArray(list) && list.length > 0) constraintRecord[key] = list;
            }
            if (values.nMax) constraints.n = { max: values.nMax };
            if (values.features?.length) constraints.features = values.features;
            const prices = Object.entries(values.prices ?? {})
                .filter(([key]) => key.length > 0)
                .map(([key, yuan]) => ({ params: Object.fromEntries((JSON.parse(key) as string[]).map((value) => [value, value])), costMicros: yuanToMicros(yuan) }));
            const creditCost: ModelCreditCost = { version: 1, dimensions: values.dimensions, prices };
            const payload = {
                channelIds: values.channelIds,
                name: values.name,
                displayName: values.displayName,
                capability: values.capability,
                provider: values.provider,
                constraints,
                creditCost,
                freeTrialEligible: values.freeTrialEligible,
                enabled: values.enabled,
                sort: values.sort,
            };
            return editing ? updateAdminModel(editing.id, payload) : createAdminModel(payload);
        },
        onSuccess: async () => {
            message.success(t("admin.models.saved"));
            closeEditor();
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const deleteMutation = useMutation({
        mutationFn: (id: string) => deleteAdminModel(id),
        onSuccess: async () => {
            message.success(t("admin.models.deleted"));
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const toggleMutation = useMutation({
        mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => updateAdminModel(id, { enabled }),
        onSuccess: async () => {
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const closeEditor = () => {
        setEditing(null);
        setCreating(false);
        form.resetFields();
    };

    const openCreate = () => {
        setCreating(true);
        setEditing(null);
        form.setFieldsValue({
            channelIds: [],
            name: "",
            displayName: "",
            capability: "image",
            provider: "openai",
            sort: 0,
            enabled: true,
            freeTrialEligible: false,
            constraints: { size: ["1024x1024"], quality: ["low", "high"] },
            nMax: 4,
            features: ["referenceImage"],
            dimensions: ["size", "quality"],
            prices: {},
        } as ModelFormValues);
    };

    const openEdit = (item: AdminModel) => {
        const constraints = (item.constraints ?? {}) as ModelConstraints;
        // 已知键之外的自定义键也一并读进表单（渲染时落入「自定义约束」分组），保证老数据打开编辑器不丢字段。
        const constraintValues: Record<string, string[]> = {};
        for (const [key, list] of Object.entries(constraints)) {
            if (key === "n" || key === "features") continue;
            if (Array.isArray(list)) {
                constraintValues[key] = list.map((option) => optionValue(option as never));
            }
        }
        const prices: Record<string, number> = {};
        for (const price of item.creditCost?.prices ?? []) {
            const parts = (item.creditCost?.dimensions ?? []).map((dimension) => String(price.params[dimension] ?? ""));
            prices[combinationKey(parts)] = microsToYuan(price.costMicros);
        }
        setEditing(item);
        setCreating(false);
        form.setFieldsValue({
            channelIds: (item.channelIds as string[] | undefined) ?? [],
            name: item.name,
            displayName: item.displayName,
            capability: item.capability,
            provider: item.provider,
            sort: item.sort,
            enabled: item.enabled,
            freeTrialEligible: item.freeTrialEligible,
            constraints: constraintValues,
            nMax: constraints.n?.max,
            features: constraints.features ?? [],
            dimensions: (item.creditCost?.dimensions as ConstraintKey[]) ?? [],
            prices,
        } as ModelFormValues);
    };

    const columns: ColumnsType<AdminModel> = [
        {
            title: t("admin.models.columns.model"),
            key: "model",
            render: (_, row) => (
                <div className="min-w-0">
                    <div className="truncate font-medium">{row.displayName}</div>
                    <div className="truncate font-mono text-xs text-stone-500 dark:text-stone-400">{row.name}</div>
                </div>
            ),
        },
        { title: t("admin.models.columns.capability"), dataIndex: "capability", width: 100, render: (value: string) => t(`admin.models.capabilities.${value}`) },
        { title: t("admin.models.columns.provider"), dataIndex: "provider", width: 110 },
        {
            title: t("admin.models.columns.prices"),
            key: "prices",
            width: 110,
            render: (_, row) => t("admin.models.priceCount", { count: row.creditCost?.prices?.length ?? 0 }),
        },
        {
            title: t("admin.models.columns.freeTrial"),
            dataIndex: "freeTrialEligible",
            width: 100,
            render: (value: boolean) => (value ? <Tag color="green">{t("admin.models.on")}</Tag> : "—"),
        },
        {
            title: t("admin.models.columns.enabled"),
            dataIndex: "enabled",
            width: 90,
            render: (value: boolean, row) => (
                <Switch size="small" checked={value} loading={toggleMutation.isPending} onChange={(checked) => toggleMutation.mutate({ id: row.id, enabled: checked })} />
            ),
        },
        {
            title: t("admin.models.columns.actions"),
            key: "actions",
            width: 200,
            render: (_, row) => (
                <Space size={2} wrap>
                    <Button size="small" type="link" className="!px-1" onClick={() => openEdit(row)}>
                        {t("admin.edit")}
                    </Button>
                    <Button size="small" type="link" className="!px-1" onClick={() => setPromotionModel(row)}>
                        {t("admin.models.promotions")}
                    </Button>
                    <Popconfirm title={t("admin.models.deleteConfirm")} description={t("admin.models.deleteHint")} onConfirm={() => deleteMutation.mutate(row.id)}>
                        <Button size="small" type="link" danger className="!px-1">
                            {t("admin.delete")}
                        </Button>
                    </Popconfirm>
                </Space>
            ),
        },
    ];

    const combinations = useMemo(() => buildCombinations(dimensions, constraintsWatch), [dimensions, constraintsWatch]);

    return (
        <div className="flex flex-col gap-4">
            <div className="flex items-center justify-between">
                <p className="text-sm text-stone-500 dark:text-stone-400">{t("admin.models.hint")}</p>
                <Button type="primary" onClick={openCreate}>
                    {t("admin.models.create")}
                </Button>
            </div>
            {modelsQuery.isError ? (
                <QueryError error={modelsQuery.error} message={t("admin.models.loadFailed")} onRetry={() => void modelsQuery.refetch()} />
            ) : (
                <>
                    <Segmented
                        value={capabilityFilter}
                        onChange={(value) => setCapabilityFilter(value as "all" | ModelCapability)}
                        options={[
                            { value: "all", label: t("admin.models.filters.labeled", { label: t("admin.models.filters.all"), count: capabilityCounts.all }) },
                            ...CAPABILITIES.map((capability) => ({
                                value: capability,
                                label: t("admin.models.filters.labeled", { label: t(`admin.models.capabilities.${capability}`), count: capabilityCounts[capability] }),
                            })),
                        ]}
                    />
                    <Table<AdminModel>
                        rowKey="id"
                        size="middle"
                        loading={modelsQuery.isPending}
                        columns={columns}
                        dataSource={filteredModels}
                        pagination={false}
                    />
                </>
            )}

            <Drawer
                open={creating || !!editing}
                width={720}
                title={editing ? t("admin.models.editTitle", { name: editing.displayName }) : t("admin.models.createTitle")}
                onClose={closeEditor}
                extra={
                    <Button type="primary" loading={saveMutation.isPending} onClick={() => void form.submit()}>
                        {t("admin.save")}
                    </Button>
                }
            >
                <Form
                    form={form}
                    layout="vertical"
                    onFinish={(values) => {
                        // 折扣活动之外的字段全部在这里校验：价格矩阵必须覆盖全部组合。
                        const combos = buildCombinations(values.dimensions ?? [], values.constraints ?? {});
                        const missing = combos.filter((parts) => {
                            const value = values.prices?.[combinationKey(parts)];
                            return value === undefined || value === null || Number.isNaN(value);
                        });
                        if (values.dimensions?.length && missing.length > 0) {
                            message.error(t("admin.models.missingPrices", { count: missing.length }));
                            return;
                        }
                        saveMutation.mutate(values);
                    }}
                >
                    <div className="grid grid-cols-2 gap-x-4">
                        <Form.Item name="name" label={t("admin.models.fields.name")} extra={t("admin.models.fields.nameExtra")} rules={[{ required: true }]}>
                            <Input maxLength={120} />
                        </Form.Item>
                        <Form.Item name="displayName" label={t("admin.models.fields.displayName")} rules={[{ required: true }]}>
                            <Input maxLength={120} />
                        </Form.Item>
                        <Form.Item name="capability" label={t("admin.models.fields.capability")} rules={[{ required: true }]}>
                            <Select options={CAPABILITIES.map((value) => ({ value, label: t(`admin.models.capabilities.${value}`) }))} />
                        </Form.Item>
                        <Form.Item name="provider" label={t("admin.models.fields.provider")} rules={[{ required: true }]}>
                            <Input maxLength={60} />
                        </Form.Item>
                        <Form.Item name="channelIds" label={t("admin.models.fields.channels")} extra={t("admin.models.fields.channelsExtra")}>
                            <Select
                                mode="multiple"
                                allowClear
                                placeholder={t("admin.models.fields.channelsPlaceholder")}
                                options={(channelsQuery.data?.items ?? []).map((channel) => ({
                                    value: channel.id,
                                    label: `${channel.name} · ${channel.apiFormat}${channel.enabled ? "" : ` (${t("admin.channels.off")})`}`,
                                }))}
                            />
                        </Form.Item>
                        <Form.Item name="sort" label={t("admin.models.fields.sort")}>
                            <InputNumber className="w-full" precision={0} />
                        </Form.Item>
                        <div className="flex items-end gap-6 pb-6">
                            <Form.Item name="enabled" label={t("admin.models.fields.enabled")} valuePropName="checked" className="!mb-0">
                                <Switch />
                            </Form.Item>
                            <Form.Item name="freeTrialEligible" label={t("admin.models.fields.freeTrial")} valuePropName="checked" className="!mb-0">
                                <Switch />
                            </Form.Item>
                        </div>
                    </div>

                    <Divider titlePlacement="start">{t("admin.models.constraintsTitle")}</Divider>
                    {/* 约束表单按能力分组渲染：tags 分组可选建议值也可自由输入，multiple 分组只能从建议值里选。 */}
                    {activeGroups.map((group) => (
                        <Form.Item
                            key={group.key}
                            name={["constraints", group.key]}
                            label={t(group.labelKey)}
                            extra={group.tags ? t("admin.models.freeInputHint") : undefined}
                        >
                            <Select
                                mode={group.tags ? "tags" : "multiple"}
                                tokenSeparators={group.tags ? [","] : undefined}
                                placeholder={t("admin.models.constraintPlaceholder")}
                                options={(group.options ?? []).map((value) => ({ value, label: value }))}
                            />
                        </Form.Item>
                    ))}
                    {/* 预置分组之外的约束键原样保留：清空取值后保存即删除该键。 */}
                    {customConstraintKeys.length > 0 ? (
                        <>
                            <Divider titlePlacement="start">{t("admin.models.customConstraintsTitle")}</Divider>
                            {customConstraintKeys.map((key) => (
                                <Form.Item key={key} name={["constraints", key]} label={key} extra={t("admin.models.customConstraintsHint")}>
                                    <Select mode="tags" tokenSeparators={[","]} open={false} placeholder={t("admin.models.constraintPlaceholder")} />
                                </Form.Item>
                            ))}
                        </>
                    ) : null}
                    {showNMax || showFeatures ? (
                        <div className="grid grid-cols-2 gap-x-4">
                            {showNMax ? (
                                <Form.Item
                                    name="nMax"
                                    label={t(activeCapability === "text" ? "admin.models.nMaxText" : "admin.models.nMaxImage")}
                                    extra={t("admin.models.nHint")}
                                >
                                    <InputNumber className="w-full" precision={0} min={1} max={15} />
                                </Form.Item>
                            ) : null}
                            {showFeatures ? (
                                <Form.Item name="features" label={t("admin.models.constraintKeys.features")}>
                                    <Select mode="multiple" options={featureOptions.map((value) => ({ value, label: t(`admin.models.features.${value}`) }))} />
                                </Form.Item>
                            ) : null}
                        </div>
                    ) : null}

                    <Divider titlePlacement="start">{t("admin.models.pricingTitle")}</Divider>
                    <Form.Item name="dimensions" label={t("admin.models.fields.dimensions")} extra={t("admin.models.dimensionsHint")}>
                        <Select
                            mode="multiple"
                            // 计价维度服务端只接受 size/ratio/resolution/quality/duration（与折扣匹配参数同口径），这里不随分组扩大。
                            options={
                                activeCapability === "video"
                                    ? [{ value: "resolution", label: t("admin.models.constraintKeys.resolution") }, { value: "duration", label: t("admin.models.constraintKeys.duration") }]
                                    : [
                                          { value: "size", label: t("admin.models.constraintKeys.size") },
                                          { value: "quality", label: t("admin.models.constraintKeys.quality") },
                                          { value: "resolution", label: t("admin.models.constraintKeys.resolution") },
                                      ]
                            }
                        />
                    </Form.Item>
                    {combinations.length > 0 ? (
                        <div className="rounded-lg border border-stone-200 dark:border-stone-800">
                            <table className="w-full text-sm">
                                <thead>
                                    <tr className="border-b border-stone-200 text-left text-xs text-stone-500 dark:border-stone-800 dark:text-stone-400">
                                        {dimensions.map((dimension) => (
                                            <th key={dimension} className="px-3 py-2 font-normal">
                                                {t(`admin.models.constraintKeys.${dimension}`)}
                                            </th>
                                        ))}
                                        <th className="w-40 px-3 py-2 font-normal">{t("admin.models.fields.priceYuan")}</th>
                                    </tr>
                                </thead>
                                <tbody>
                                    {combinations.map((parts) => (
                                        <tr key={combinationKey(parts)} className="border-b border-stone-100 last:border-0 dark:border-stone-800/60">
                                            {parts.map((value, index) => (
                                                <td key={index} className="px-3 py-2">
                                                    {value}
                                                </td>
                                            ))}
                                            <td className="px-3 py-2">
                                                <Form.Item name={["prices", combinationKey(parts)]} className="!mb-0" rules={[{ required: true, message: t("admin.models.priceRequired") }]}>
                                                    <InputNumber className="w-full" min={0} step={0.1} precision={4} />
                                                </Form.Item>
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    ) : (
                        <p className="text-xs text-stone-500 dark:text-stone-400">{t("admin.models.noCombinations")}</p>
                    )}
                </Form>
            </Drawer>

            <PromotionDrawer model={promotionModel} onClose={() => setPromotionModel(null)} />
        </div>
    );
}

function PromotionDrawer({ model, onClose }: { model: AdminModel | null; onClose: () => void }) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const queryClient = useQueryClient();
    const [editing, setEditing] = useState<AdminPromotion | null>(null);
    const [form] = Form.useForm();

    const promotionsQuery = useQuery({
        queryKey: ["admin", "promotions", model?.id],
        queryFn: ({ signal }) => listAdminPromotions({ modelId: model?.id }, signal),
        enabled: !!model,
    });

    const constraintKeys = useMemo(() => {
        if (!model?.constraints) return [] as ConstraintKey[];
        return CONSTRAINT_KEYS.filter((key) => {
            const list = (model.constraints as ModelConstraints)[key];
            return Array.isArray(list) && list.length > 0;
        });
    }, [model]);

    const invalidate = async () => {
        await Promise.all([
            queryClient.invalidateQueries({ queryKey: ["admin", "promotions", model?.id] }),
            queryClient.invalidateQueries({ queryKey: ["models"] }),
        ]);
    };

    const saveMutation = useMutation({
        mutationFn: (values: { name: string; matchParams: Record<string, string>; discountBps: number; priority: number; startsAt: string; endsAt: string; reason?: string }) => {
            const input = {
                modelId: model!.id,
                name: values.name,
                matchParams: values.matchParams ?? {},
                discountBps: values.discountBps,
                priority: values.priority,
                startsAt: new Date(values.startsAt).toISOString(),
                endsAt: new Date(values.endsAt).toISOString(),
                reason: values.reason,
            };
            return editing ? updateAdminPromotion(editing.id, input) : createAdminPromotion(input);
        },
        onSuccess: async () => {
            message.success(t("admin.promotions.saved"));
            setEditing(null);
            form.resetFields();
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const disableMutation = useMutation({
        mutationFn: (id: string) => updateAdminPromotion(id, { status: "disabled" }),
        onSuccess: async () => {
            message.success(t("admin.promotions.disabled"));
            await invalidate();
        },
        onError: (error) => message.error(getApiErrorMessage(error)),
    });

    const columns: ColumnsType<AdminPromotion> = [
        { title: t("admin.promotions.columns.name"), dataIndex: "name" },
        {
            title: t("admin.promotions.columns.match"),
            dataIndex: "matchParams",
            render: (value: Record<string, string>) =>
                Object.entries(value ?? {}).length === 0 ? t("admin.promotions.wholeModel") : Object.entries(value).map(([key, item]) => `${key}=${item}`).join(", "),
        },
        { title: t("admin.promotions.columns.discount"), dataIndex: "discountBps", width: 100, render: (value: number) => `${value / 1000} 折` },
        { title: t("admin.promotions.columns.priority"), dataIndex: "priority", width: 80 },
        { title: t("admin.promotions.columns.version"), dataIndex: "version", width: 80 },
        {
            title: t("admin.promotions.columns.window"),
            key: "window",
            width: 220,
            render: (_, row) => `${dayjs(row.startsAt).format("MM-DD HH:mm")} ~ ${dayjs(row.endsAt).format("MM-DD HH:mm")}`,
        },
        {
            title: t("admin.promotions.columns.status"),
            dataIndex: "status",
            width: 100,
            render: (value: string) => <Tag>{t(`admin.promotions.statuses.${value}`)}</Tag>,
        },
        {
            title: t("admin.promotions.columns.actions"),
            key: "actions",
            width: 150,
            render: (_, row) => (
                <Space size={2}>
                    <Button
                        size="small"
                        type="link"
                        className="!px-1"
                        onClick={() => {
                            setEditing(row);
                            form.setFieldsValue({
                                name: row.name,
                                discountBps: row.discountBps,
                                priority: row.priority,
                                matchParams: Object.entries(row.matchParams ?? {}).reduce<Record<string, string>>((acc, [key, value]) => ({ ...acc, [key]: value }), {}),
                                // datetime-local 需要 `YYYY-MM-DDTHH:mm` 形式的本地时间字符串。
                                startsAt: dayjs(row.startsAt).format("YYYY-MM-DDTHH:mm"),
                                endsAt: dayjs(row.endsAt).format("YYYY-MM-DDTHH:mm"),
                            });
                        }}
                    >
                        {t("admin.edit")}
                    </Button>
                    {row.status !== "disabled" && row.status !== "ended" ? (
                        <Popconfirm title={t("admin.promotions.disableConfirm")} onConfirm={() => disableMutation.mutate(row.id)}>
                            <Button size="small" type="link" danger className="!px-1">
                                {t("admin.promotions.disable")}
                            </Button>
                        </Popconfirm>
                    ) : null}
                </Space>
            ),
        },
    ];

    return (
        <Drawer open={!!model} width={720} title={t("admin.promotions.title", { name: model?.displayName ?? "" })} onClose={onClose}>
            <Alert className="mb-4" type="info" showIcon message={t("admin.promotions.hint")} />
            <Form
                form={form}
                layout="vertical"
                initialValues={{ discountBps: 9000, priority: 0, matchParams: {}, startsAt: dayjs().format("YYYY-MM-DDTHH:mm"), endsAt: dayjs().add(7, "day").format("YYYY-MM-DDTHH:mm") }}
                onFinish={(values) => saveMutation.mutate(values)}
                className="rounded-lg border border-stone-200 p-4 dark:border-stone-800"
            >
                <div className="grid grid-cols-2 gap-x-4">
                    <Form.Item name="name" label={t("admin.promotions.fields.name")} rules={[{ required: true }]}>
                        <Input maxLength={120} />
                    </Form.Item>
                    <Form.Item name="discountBps" label={t("admin.promotions.fields.discountBps")} extra={t("admin.promotions.fields.discountHint")} rules={[{ required: true }]}>
                        <InputNumber className="w-full" min={1} max={9999} precision={0} />
                    </Form.Item>
                    <Form.Item name="startsAt" label={t("admin.promotions.fields.startsAt")} rules={[{ required: true }]}>
                        <Input type="datetime-local" />
                    </Form.Item>
                    <Form.Item name="endsAt" label={t("admin.promotions.fields.endsAt")} rules={[{ required: true }]}>
                        <Input type="datetime-local" />
                    </Form.Item>
                    <Form.Item name="priority" label={t("admin.promotions.fields.priority")}>
                        <InputNumber className="w-full" precision={0} />
                    </Form.Item>
                </div>
                {constraintKeys.map((key) => (
                    <Form.Item key={key} name={["matchParams", key]} label={t("admin.promotions.matchLabel", { key: t(`admin.models.constraintKeys.${key}`) })}>
                        <Select
                            allowClear
                            placeholder={t("admin.promotions.matchPlaceholder")}
                            options={(((model?.constraints as ModelConstraints)?.[key] ?? []) as unknown[]).map((option) => {
                                const value = optionValue(option as never);
                                return { value, label: value };
                            })}
                        />
                    </Form.Item>
                ))}
                <Form.Item name="reason" label={t("admin.promotions.fields.reason")}>
                    <Input maxLength={200} />
                </Form.Item>
                <div className="flex justify-end gap-2">
                    {editing ? (
                        <Button
                            onClick={() => {
                                setEditing(null);
                                form.resetFields();
                            }}
                        >
                            {t("admin.cancel")}
                        </Button>
                    ) : null}
                    <Button type="primary" htmlType="submit" loading={saveMutation.isPending}>
                        {editing ? t("admin.promotions.saveVersion") : t("admin.promotions.create")}
                    </Button>
                </div>
            </Form>

            {promotionsQuery.isError ? (
                <QueryError className="mt-4" error={promotionsQuery.error} message={t("admin.promotions.loadFailed")} />
            ) : (
                <Table<AdminPromotion>
                    className="mt-4"
                    rowKey="id"
                    size="small"
                    loading={promotionsQuery.isPending}
                    columns={columns}
                    dataSource={promotionsQuery.data?.items ?? []}
                    pagination={false}
                />
            )}

            <Descriptions className="mt-6" size="small" column={1} title={t("admin.promotions.basePrices")}>
                {(model?.creditCost?.prices ?? []).slice(0, 20).map((price) => {
                    const params = (model?.creditCost?.dimensions ?? []).map((dimension) => price.params[dimension]).join(" / ");
                    return (
                        <Descriptions.Item key={params} label={params}>
                            {formatMoney(price.costMicros)} · {formatPoints(price.costMicros)}
                        </Descriptions.Item>
                    );
                })}
            </Descriptions>
        </Drawer>
    );
}
