import { apiRequest } from "@/services/api/client";

export type PaymentProvider = "alipay" | "wechat";
export type PaymentType = "qrcode" | "redirect" | "clientSecret";
export type OrderStatus = "pending" | "paid" | "failed" | "refunded";

export type Order = {
    id: string;
    provider?: PaymentProvider;
    providerOrderId?: string | null;
    packageId: string;
    priceMicros: number;
    currency: string;
    purchasedMicros: number;
    grantedMicros: number;
    entitlementDays: number;
    status: OrderStatus;
    paidAt?: string | null;
    createdAt: string;
    updatedAt?: string;
};

export type PaymentParams = {
    type: PaymentType;
    payload: string;
};

export type OrderPage = {
    items: Order[];
    nextCursor: string | null;
};

export type CreateOrderResponse = {
    order: Order;
    payment: PaymentParams;
};

export function createOrder(input: { packageId: string; provider: PaymentProvider }, signal?: AbortSignal) {
    return apiRequest<CreateOrderResponse>("/orders", { method: "POST", body: input, signal });
}

export function listOrders(params: { cursor?: string; size?: number; status?: OrderStatus }, signal?: AbortSignal) {
    return apiRequest<OrderPage>("/orders", {
        query: { cursor: params.cursor, size: params.size, status: params.status },
        signal,
    });
}

export async function cancelOrder(id: string, signal?: AbortSignal) {
    const result = await apiRequest<Order | { order: Order }>(`/orders/${id}/cancel`, { method: "POST", signal });
    return "order" in result ? result.order : result;
}

export async function getOrder(orderId: string, signal?: AbortSignal) {
    const page = await listOrders({ size: 50 }, signal);
    return page.items.find((order) => order.id === orderId) ?? null;
}
