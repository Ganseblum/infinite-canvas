import { apiRequest } from "@/services/api/client";

export type PaymentProvider = "alipay" | "wechat" | "easypay";
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

// 点数充值订单接口客户端（/api/v1/orders）：创建订单 → 按支付参数拉起支付 → 轮询订单状态。

/** 创建充值订单，返回订单与支付参数（二维码/跳转/收银台，取决于渠道）。POST /api/v1/orders。 */
export function createOrder(input: { packageId: string; provider: PaymentProvider }, signal?: AbortSignal) {
    return apiRequest<CreateOrderResponse>("/orders", { method: "POST", body: input, signal });
}

/** 游标分页查询我的订单，可按状态过滤。GET /api/v1/orders。 */
export function listOrders(params: { cursor?: string; size?: number; status?: OrderStatus }, signal?: AbortSignal) {
    return apiRequest<OrderPage>("/orders", {
        query: { cursor: params.cursor, size: params.size, status: params.status },
        signal,
    });
}

/** 取消未支付订单；响应体两种历史形态都兼容。POST /api/v1/orders/{id}/cancel。 */
export async function cancelOrder(id: string, signal?: AbortSignal) {
    const result = await apiRequest<Order | { order: Order }>(`/orders/${id}/cancel`, { method: "POST", signal });
    return "order" in result ? result.order : result;
}

/** 按 id 查单个订单：列表接口没有按 id 查询的端点，取最近一页在客户端过滤。 */
export async function getOrder(orderId: string, signal?: AbortSignal) {
    const page = await listOrders({ size: 50 }, signal);
    return page.items.find((order) => order.id === orderId) ?? null;
}
