import type { ReferenceImage } from "@/types/image";

import i18n from "@/i18n";

/** 参考图的序号标签（图 1、图 2…），与画布资源引用、Agent 提示词共用同一套编号。 */
export function imageReferenceLabel(index: number) {
    return i18n.t("imageReferences.label", { index: index + 1 });
}

/** 把参考图编号拼进提示词开头（如「图 1、图 2：提示词」），无参考图时原样返回。 */
export function buildImageReferencePromptText(prompt: string, references: ReferenceImage[]) {
    const text = prompt.trim();
    if (!references.length) return text;
    const labels = references.map((_, index) => imageReferenceLabel(index));
    return i18n.t("imageReferences.promptPrefix", { labels: labels.join(i18n.t("imageReferences.separator")), prompt: text });
}
