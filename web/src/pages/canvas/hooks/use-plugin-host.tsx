import { useCallback, useEffect, useMemo, type Dispatch, type MutableRefObject, type SetStateAction } from "react";
import { useTranslation } from "react-i18next";

import { requestEdit, requestGeneration, requestImageQuestion, type AiTextMessage } from "@/services/api/image";
import { requestVideoGeneration } from "@/services/api/video";
import { buildGenerationConfig } from "@/lib/canvas/canvas-generation-helpers";
import { buildNodeContext } from "@/lib/canvas/plugin-node-context";
import { getNodeDefinition } from "@/lib/canvas/node-registry";
import { ensurePluginsLoaded } from "@/lib/canvas/plugin-loader";
import { canvasThemes } from "@/lib/canvas-theme";
import { catalogModel, isModelCatalogReady, selectableModelsByCapability } from "@/stores/use-model-catalog-store";
import type { AiConfig, ModelCapability } from "@/stores/use-config-store";
import type { CanvasNodeToolbarItem, CanvasPluginAi, CanvasPluginHost } from "@/types/canvas-plugin";
import type { ReferenceImage } from "@/types/image";
import type { CanvasAgentOp } from "@/lib/canvas/canvas-agent-ops";
import type { CanvasConnection, CanvasNodeData, ViewportTransform } from "@/types/canvas";

type CanvasTheme = (typeof canvasThemes)[keyof typeof canvasThemes];

/**
 * 插件宿主参数：把画布读写能力（ref + setState）与生成配置交给插件节点使用。
 */
type PluginHostParams = {
    effectiveConfig: AiConfig;
    theme: CanvasTheme;
    nodesRef: MutableRefObject<CanvasNodeData[]>;
    connectionsRef: MutableRefObject<CanvasConnection[]>;
    viewportRef: MutableRefObject<ViewportTransform>;
    setNodes: Dispatch<SetStateAction<CanvasNodeData[]>>;
    setDialogNodeId: Dispatch<SetStateAction<string | null>>;
    applyAgentOps: (ops?: CanvasAgentOp[]) => unknown;
};

/**
 * Plugin node host capabilities: expose host-side AI generation, canvas access, and panel controls
 * through plugin-callable host/ai objects. Loads installed remote plugins on mount and returns renderers for plugin panels and toolbars.
 */
export function usePluginHost(params: PluginHostParams) {
    const { t } = useTranslation();
    const { effectiveConfig, theme, nodesRef, connectionsRef, viewportRef, setNodes, setDialogNodeId, applyAgentOps } = params;

    // Host capabilities available to plugin nodes; methods receive nodeId and are not bound to a specific node.
    const pluginAi = useMemo<CanvasPluginAi>(() => {
        // Convert plugin reference images (data URLs or URLs) into the ReferenceImage[] expected by the host generation API.
        const toReferences = (refs?: string[]): ReferenceImage[] => (refs || []).filter(Boolean).map((src, index) => ({ id: `plugin-ref-${index}`, name: `ref-${index}.png`, type: "image/png", dataUrl: src }));
        const ensureReady = (config: AiConfig) => {
            if (!isModelCatalogReady(config.model)) throw new Error(t("canvas.plugins.aiConfigRequired"));
        };
        return {
            generateImage: async (prompt, options) => {
                const config = { ...buildGenerationConfig(effectiveConfig, undefined, "image"), count: String(options?.count || 1), ...(options?.model ? { model: options.model } : {}), ...(options?.size ? { size: options.size } : {}) };
                ensureReady(config);
                const references = toReferences(options?.references);
                const items = references.length ? await requestEdit(config, prompt, references, { signal: options?.signal }) : await requestGeneration(config, prompt, { signal: options?.signal });
                return { images: items.map((item) => item.dataUrl) };
            },
            generateVideo: async (prompt, options) => {
                const config = {
                    ...buildGenerationConfig(effectiveConfig, undefined, "video"),
                    ...(options?.model ? { model: options.model } : {}),
                    ...(options?.size ? { size: options.size } : {}),
                    ...(options?.seconds ? { videoSeconds: options.seconds } : {}),
                };
                ensureReady(config);
                const video = await requestVideoGeneration(config, prompt, toReferences(options?.references), { signal: options?.signal });
                return { url: video.url, mimeType: video.mimeType, width: video.width, height: video.height, durationMs: video.durationMs };
            },
            generateText: async (prompt, options) => {
                const config = { ...buildGenerationConfig(effectiveConfig, undefined, "text"), ...(options?.model ? { model: options.model } : {}) };
                ensureReady(config);
                const messages: AiTextMessage[] = [...(options?.system ? [{ role: "system" as const, content: options.system }] : []), { role: "user" as const, content: prompt }];
                const text = await requestImageQuestion(config, messages, (delta) => options?.onDelta?.(delta), { signal: options?.signal });
                return { text };
            },
            // List platform catalog models for a capability.
            listModels: (capability) => selectableModelsByCapability(capability as ModelCapability | undefined).map((value) => ({ value, label: catalogModel(value)?.displayName || value })),
            defaultModel: (capability) => buildGenerationConfig(effectiveConfig, undefined, capability).model,
        };
    }, [effectiveConfig, t]);

    const pluginHost = useMemo<CanvasPluginHost>(
        () => ({
            getNode: (id) => nodesRef.current.find((node) => node.id === id) || null,
            getNodes: () => nodesRef.current,
            getConnections: () => connectionsRef.current,
            getUpstream: (nodeId) =>
                connectionsRef.current
                    .filter((conn) => conn.toNodeId === nodeId)
                    .map((conn) => nodesRef.current.find((node) => node.id === conn.fromNodeId))
                    .filter((node): node is CanvasNodeData => Boolean(node)),
            getDownstream: (nodeId) =>
                connectionsRef.current
                    .filter((conn) => conn.fromNodeId === nodeId)
                    .map((conn) => nodesRef.current.find((node) => node.id === conn.toNodeId))
                    .filter((node): node is CanvasNodeData => Boolean(node)),
            updateNode: (nodeId, patch) => setNodes((prev) => prev.map((node) => (node.id === nodeId ? { ...node, ...patch } : node))),
            updateMetadata: (nodeId, patch) => setNodes((prev) => prev.map((node) => (node.id === nodeId ? { ...node, metadata: { ...node.metadata, ...patch } } : node))),
            applyOps: (ops) => applyAgentOps(ops),
            ai: pluginAi,
            openPanel: (nodeId) => setDialogNodeId(nodeId),
            closePanel: () => setDialogNodeId(null),
        }),
        [applyAgentOps, pluginAi],
    );

    // 渲染插件节点弹出的自定义面板；插件未定义 Panel 时返回 null。
    const renderPluginPanel = useCallback(
        (panelNode: CanvasNodeData) => {
            const Panel = getNodeDefinition(panelNode.type)?.Panel;
            if (!Panel) return null;
            const ctx = buildNodeContext(pluginHost, panelNode, theme, viewportRef.current.k);
            return <Panel ctx={ctx} onClose={() => setDialogNodeId(null)} />;
        },
        [pluginHost, theme],
    );

    // Build the node toolbar from plugin items and a host-provided interaction/move toggle when enabled.
    const buildNodeToolbarItems = useCallback(
        (node: CanvasNodeData): CanvasNodeToolbarItem[] => {
            const definition = getNodeDefinition(node.type);
            const ctx = buildNodeContext(pluginHost, node, theme, viewportRef.current.k);
            const custom = definition?.toolbar?.(ctx) || [];
            // Show the interaction/move toggle only for nodes with content that are not forced into an interactive state.
            if (!definition?.interactionToggle || !node.metadata?.content || definition.forceInteractive?.(node)) return custom;
            const interactive = Boolean(node.metadata?.interactive);
            const toggle: CanvasNodeToolbarItem = {
                id: "node-interaction-toggle",
                title: t(interactive ? "canvas.plugins.interactiveTitle" : "canvas.plugins.movableTitle"),
                label: t(interactive ? "canvas.plugins.move" : "canvas.plugins.interact"),
                icon: interactive ? "✋" : "🖐",
                active: interactive,
                onClick: () => pluginHost.updateMetadata(node.id, { interactive: !interactive }),
            };
            return [toggle, ...custom];
        },
        [pluginHost, t, theme],
    );

    // Load installed remote plugins on startup.
    useEffect(() => {
        void ensurePluginsLoaded();
    }, []);

    return { pluginHost, renderPluginPanel, buildNodeToolbarItems };
}
