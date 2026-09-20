// 点数格式化工具：后端以「微元」计（1 点 = 1_000_000 micros），展示时只做千分位/货币换算。
const MICROS_PER_CURRENCY_UNIT = 1_000_000;

/** 微元取整并加千分位，作为「点数」展示。 */
export function formatPoints(micros: number): string {
    return Math.round(micros).toLocaleString();
}

/** 带符号的点数（正数加 +），流水/增减展示用。 */
export function formatSignedPoints(micros: number): string {
    const rounded = Math.round(micros);
    const sign = rounded > 0 ? "+" : rounded < 0 ? "-" : "";
    return `${sign}${Math.abs(rounded).toLocaleString()}`;
}

/** 微元换算成货币金额（两位小数），CNY 加 ¥ 前缀。 */
export function formatMoney(micros: number, currency = "CNY"): string {
    const amount = (micros / MICROS_PER_CURRENCY_UNIT).toFixed(2);
    return currency === "CNY" ? `¥${amount}` : `${currency} ${amount}`;
}

/** 折扣展示：discount 为折扣倍率（如 8.5），percentOff 为让利百分比（如 15）。 */
export function formatDiscount(discountBps: number): { discount: string; percentOff: string } {
    const discount = discountBps / 1000;
    return {
        discount: Number.isInteger(discount) ? String(discount) : discount.toFixed(1),
        percentOff: (100 - discountBps / 100).toFixed(0),
    };
}
