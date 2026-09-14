const MIN_PASSWORD_BYTES = 8;
const MAX_PASSWORD_BYTES = 72;

// 后端用 len(password) 校验，按 UTF-8 字节数计；前端表单必须用同一口径。
export function isPasswordByteLengthValid(value: string) {
    const bytes = new TextEncoder().encode(value).length;
    return bytes >= MIN_PASSWORD_BYTES && bytes <= MAX_PASSWORD_BYTES;
}
