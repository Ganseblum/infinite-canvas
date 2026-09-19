"use client";

import { useEffect } from "react";

import { bootstrapSession } from "@/lib/auth-client";

// 冷加载会话引导：挂载在根 layout，页面加载即静默续期共享会话；
// 匿名访客失败静默，不渲染任何内容。
export default function AuthBootstrap() {
  useEffect(() => {
    void bootstrapSession();
  }, []);
  return null;
}
