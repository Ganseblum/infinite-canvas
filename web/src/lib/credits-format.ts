const MICROS_PER_CURRENCY_UNIT = 1_000_000;

export function formatPoints(micros: number): string {
    return Math.round(micros).toLocaleString();
}

export function formatSignedPoints(micros: number): string {
    const rounded = Math.round(micros);
    const sign = rounded > 0 ? "+" : rounded < 0 ? "-" : "";
    return `${sign}${Math.abs(rounded).toLocaleString()}`;
}

export function formatMoney(micros: number, currency = "CNY"): string {
    const amount = (micros / MICROS_PER_CURRENCY_UNIT).toFixed(2);
    return currency === "CNY" ? `¥${amount}` : `${currency} ${amount}`;
}

export function formatDiscount(discountBps: number): { discount: string; percentOff: string } {
    const discount = discountBps / 1000;
    return {
        discount: Number.isInteger(discount) ? String(discount) : discount.toFixed(1),
        percentOff: (100 - discountBps / 100).toFixed(0),
    };
}
