import { App } from "antd";
import copy from "copy-to-clipboard";
import { useTranslation } from "react-i18next";

/**
 * 复制文本到剪贴板并弹全局提示。
 *
 * @param successText 成功提示文案，默认取 i18n 的「已复制」。
 * @returns 立即可调用的复制函数，无返回值。
 */
export function useCopyText() {
    const { message } = App.useApp();
    const { t } = useTranslation();

    return (value: string, successText = t("common.copied")) => {
        copy(value);
        message.success(successText);
    };
}
