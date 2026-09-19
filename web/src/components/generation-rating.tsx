import { useState } from "react";
import { App, Button, Checkbox, Input, Modal } from "antd";
import { ThumbsDown, ThumbsUp } from "lucide-react";
import { useTranslation } from "react-i18next";

import { getApiErrorMessage } from "@/lib/api-error";
import { deleteGenerationFeedback, setGenerationFeedback } from "@/services/api/feedback";

export type GenerationRatingValue = 1 | -1 | null | undefined;

type RatingMeta = { labels?: string; note?: string };

// 生成结果点赞点踩：点赞直接提交，点踩弹预设标签 + 补充说明的反馈弹窗；
// 点击当前已选中的按钮撤销反馈。评分落 generation_feedbacks，后台工单台可查。
export function GenerationRating({
    generationId,
    value,
    onChange,
}: {
    generationId?: string;
    value: GenerationRatingValue;
    onChange: (rating: GenerationRatingValue, meta?: RatingMeta) => void;
}) {
    const { t } = useTranslation();
    const { message } = App.useApp();
    const [dialogOpen, setDialogOpen] = useState(false);
    const [labels, setLabels] = useState<string[]>([]);
    const [note, setNote] = useState("");
    const [submitting, setSubmitting] = useState(false);

    if (!generationId) return null;

    const rate = async (rating: GenerationRatingValue, meta?: RatingMeta) => {
        try {
            if (rating === null) await deleteGenerationFeedback(generationId);
            else if (rating === 1 || rating === -1) await setGenerationFeedback(generationId, { rating, labels: meta?.labels, note: meta?.note });
            else return;
            onChange(rating, meta);
            if (rating === -1) message.success(t("workbench.ratingSubmitted"));
        } catch (error) {
            message.error(getApiErrorMessage(error));
        }
    };

    const onLike = () => {
        if (value === 1) void rate(null);
        else void rate(1);
    };

    const onDislike = () => {
        if (value === -1) {
            void rate(null);
            return;
        }
        setLabels([]);
        setNote("");
        setDialogOpen(true);
    };

    const submitDislike = async () => {
        setSubmitting(true);
        try {
            await rate(-1, { labels: labels.join(","), note });
            setDialogOpen(false);
        } finally {
            setSubmitting(false);
        }
    };

    const presetLabels = t("workbench.ratingLabels", { returnObjects: true }) as string[];

    return (
        <>
            <span className="ml-auto flex items-center gap-0.5">
                <Button
                    size="small"
                    type="text"
                    className="!h-6 !px-1"
                    title={t("workbench.ratingLike")}
                    icon={<ThumbsUp className={`size-3.5 ${value === 1 ? "fill-emerald-500 text-emerald-500" : ""}`} />}
                    onClick={() => void onLike()}
                />
                <Button
                    size="small"
                    type="text"
                    className="!h-6 !px-1"
                    title={t("workbench.ratingDislike")}
                    icon={<ThumbsDown className={`size-3.5 ${value === -1 ? "fill-rose-500 text-rose-500" : ""}`} />}
                    onClick={onDislike}
                />
            </span>
            <Modal
                title={t("workbench.ratingDialogTitle")}
                open={dialogOpen}
                okText={t("workbench.ratingSubmit")}
                cancelText={t("common.cancel")}
                confirmLoading={submitting}
                onCancel={() => setDialogOpen(false)}
                onOk={() => void submitDislike()}
            >
                <div className="space-y-3 pt-2">
                    <p className="text-sm text-stone-500 dark:text-stone-400">{t("workbench.ratingDialogHint")}</p>
                    <Checkbox.Group
                        className="flex flex-wrap gap-2"
                        value={labels}
                        onChange={(values) => setLabels(values.map(String))}
                        options={presetLabels.map((label) => ({ value: label, label }))}
                    />
                    <Input.TextArea rows={3} maxLength={500} value={note} placeholder={t("workbench.ratingNotePlaceholder")} onChange={(event) => setNote(event.target.value)} />
                </div>
            </Modal>
        </>
    );
}
