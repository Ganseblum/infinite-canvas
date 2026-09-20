import { NextResponse } from "next/server";
import { revalidatePath, revalidateTag } from "next/cache";

// 按需再验证入口：Go 侧发布/下架/删除/栏目变更后回调这里（共享密钥），
// 失效对应缓存标签与路径，后台改完前台秒级生效。
// 兜底：回调失败时页面缓存按 revalidate 常量自动过期。
export async function POST(request: Request) {
  // 未配置密钥时直接拒绝，避免再验证端点在无鉴权状态下裸奔
  const secret = process.env.REVALIDATE_SECRET ?? "";
  if (!secret || request.headers.get("X-Revalidate-Secret") !== secret) {
    return NextResponse.json({ error: "invalid secret" }, { status: 401 });
  }
  let payload: { tags?: string[]; paths?: string[] };
  try {
    payload = (await request.json()) as { tags?: string[]; paths?: string[] };
  } catch {
    return NextResponse.json({ error: "invalid body" }, { status: 400 });
  }
  for (const tag of payload.tags ?? []) {
    revalidateTag(tag);
  }
  for (const path of payload.paths ?? []) {
    revalidatePath(path);
  }
  return NextResponse.json({ revalidated: true, now: Date.now() });
}
