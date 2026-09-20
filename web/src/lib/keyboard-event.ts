type NativeKeyboardEventLike = {
    isComposing?: boolean;
    keyCode?: number;
    which?: number;
};

type KeyboardEventLike = NativeKeyboardEventLike & {
    key?: string;
    shiftKey?: boolean;
    ctrlKey?: boolean;
    metaKey?: boolean;
    nativeEvent?: NativeKeyboardEventLike;
};

/** 判断按键是否处于输入法组词中（isComposing 或 keyCode/which 229），组词期间的回车不应触发提交。 */
export function isImeComposing(event: KeyboardEventLike) {
    const nativeEvent = event.nativeEvent;
    return Boolean(event.isComposing || nativeEvent?.isComposing || event.keyCode === 229 || event.which === 229 || nativeEvent?.keyCode === 229 || nativeEvent?.which === 229);
}

/** 是否为「裸回车」：不带 Shift/Ctrl/Meta 且不在输入法组词中，用于 Enter 发送语义。 */
export function isPlainEnterKey(event: KeyboardEventLike) {
    return event.key === "Enter" && !event.shiftKey && !event.ctrlKey && !event.metaKey && !isImeComposing(event);
}
