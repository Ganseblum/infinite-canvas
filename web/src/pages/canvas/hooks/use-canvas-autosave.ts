import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { ApiError } from "@/lib/api-error";
import { updateCanvas } from "@/services/api/canvas";
import type { CanvasData } from "@/services/data/types";

export type CanvasSaveState = "idle" | "saving" | "saved" | "error";

// 空闲 2 秒落盘；连续操作时最多 30 秒强制保存一次，避免长时间拖拽期间一次都没保存。
const IDLE_DELAY_MS = 2000;
const MAX_DELAY_MS = 30000;

type CanvasAutosaveOptions = {
    canvasId: string;
    readData: () => CanvasData;
    onConflict: (revision: number) => void;
};

// 画布自动保存链路：串行 PUT、revision 乐观锁、冲突回调与失败重试。
// 数据内容与上次保存一致时直接跳过请求，加载完成后的首批状态变化不会立刻产生一次多余保存。
export function useCanvasAutosave({ canvasId, readData, onConflict }: CanvasAutosaveOptions) {
    const [saveState, setSaveState] = useState<CanvasSaveState>("idle");
    const revisionRef = useRef(0);
    const baselineRef = useRef<string | null>(null);
    const dirtyRef = useRef(false);
    const pausedRef = useRef(false);
    const savingRef = useRef(false);
    const idleTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const maxTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const readDataRef = useRef(readData);
    const conflictRef = useRef(onConflict);

    useEffect(() => {
        readDataRef.current = readData;
        conflictRef.current = onConflict;
    });

    const clearTimers = useCallback(() => {
        if (idleTimerRef.current) {
            clearTimeout(idleTimerRef.current);
            idleTimerRef.current = null;
        }
        if (maxTimerRef.current) {
            clearTimeout(maxTimerRef.current);
            maxTimerRef.current = null;
        }
    }, []);

    const save = useCallback(async (): Promise<void> => {
        if (savingRef.current || pausedRef.current || !dirtyRef.current) return;
        const payload = readDataRef.current();
        const snapshot = JSON.stringify(payload);
        if (snapshot === baselineRef.current) {
            dirtyRef.current = false;
            setSaveState("saved");
            return;
        }

        clearTimers();
        savingRef.current = true;
        setSaveState("saving");
        try {
            const result = await updateCanvas(canvasId, { data: payload, revision: revisionRef.current });
            revisionRef.current = result.revision;
            baselineRef.current = snapshot;
            savingRef.current = false;
            if (pausedRef.current) {
                // 保存期间弹出了冲突选择框：保留脏标记，等用户处理完再继续。
                setSaveState("error");
                return;
            }
            if (dirtyRef.current) {
                // 保存期间又产生了改动：立刻补发一次，不等待下一个防抖周期。
                void save();
                return;
            }
            setSaveState("saved");
        } catch (error) {
            savingRef.current = false;
            dirtyRef.current = true;
            if (error instanceof ApiError && error.code === "REVISION_CONFLICT") {
                pausedRef.current = true;
                setSaveState("error");
                conflictRef.current(typeof error.revision === "number" ? error.revision : revisionRef.current);
                return;
            }
            setSaveState("error");
        }
    }, [canvasId, clearTimers]);

    const schedule = useCallback(() => {
        if (idleTimerRef.current) clearTimeout(idleTimerRef.current);
        idleTimerRef.current = setTimeout(() => {
            idleTimerRef.current = null;
            void save();
        }, IDLE_DELAY_MS);
        if (!maxTimerRef.current) {
            maxTimerRef.current = setTimeout(() => {
                maxTimerRef.current = null;
                void save();
            }, MAX_DELAY_MS);
        }
    }, [save]);

    const markDirty = useCallback(() => {
        dirtyRef.current = true;
        if (pausedRef.current || savingRef.current) return;
        schedule();
    }, [schedule]);

    // 详情加载完成（或冲突后重载）时重置基线，revision 以服务端返回为准。
    const load = useCallback(
        (revision: number, data: CanvasData) => {
            clearTimers();
            revisionRef.current = revision;
            baselineRef.current = JSON.stringify(data);
            dirtyRef.current = false;
            pausedRef.current = false;
            savingRef.current = false;
            setSaveState("idle");
        },
        [clearTimers],
    );

    const pause = useCallback(() => {
        pausedRef.current = true;
        clearTimers();
    }, [clearTimers]);

    const resume = useCallback(() => {
        pausedRef.current = false;
        if (dirtyRef.current) schedule();
    }, [schedule]);

    // 冲突选择「用我的版本覆盖」：以服务端返回的新 revision 为基线重发。
    const overwrite = useCallback(
        (revision: number) => {
            revisionRef.current = revision;
            pausedRef.current = false;
            dirtyRef.current = true;
            void save();
        },
        [save],
    );

    const retry = useCallback(() => {
        pausedRef.current = false;
        dirtyRef.current = true;
        void save();
    }, [save]);

    const flush = useCallback(() => {
        if (dirtyRef.current && !pausedRef.current) void save();
    }, [save]);

    useEffect(() => {
        const onHidden = () => {
            if (document.visibilityState === "hidden") flush();
        };
        document.addEventListener("visibilitychange", onHidden);
        window.addEventListener("pagehide", flush);
        return () => {
            document.removeEventListener("visibilitychange", onHidden);
            window.removeEventListener("pagehide", flush);
        };
    }, [flush]);

    useEffect(() => () => flush(), [flush]);

    // 依赖这些回调的 effect 会跟着画布状态每帧重跑，返回值必须保持稳定。
    return useMemo(() => ({ saveState, markDirty, load, pause, resume, overwrite, retry, flush }), [saveState, markDirty, load, pause, resume, overwrite, retry, flush]);
}
