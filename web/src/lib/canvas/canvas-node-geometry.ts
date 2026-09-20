import { CanvasNodeType, type CanvasConnection, type CanvasNodeData, type ConnectionHandle } from "@/types/canvas";

/**
 * 画布节点几何与分组工具：坐标一律是画布世界坐标（px），position 为节点左上角。
 * 分组归属按「节点中心点是否落在组矩形内」判定，与视觉直觉一致。
 */
export function nodeBounds(nodes: CanvasNodeData[]) {
    return nodes.reduce(
        (acc, node) => ({
            left: Math.min(acc.left, node.position.x),
            top: Math.min(acc.top, node.position.y),
            right: Math.max(acc.right, node.position.x + node.width),
            bottom: Math.max(acc.bottom, node.position.y + node.height),
        }),
        { left: Infinity, top: Infinity, right: -Infinity, bottom: -Infinity },
    );
}

function containsCenter(group: CanvasNodeData, node: CanvasNodeData) {
    const centerX = node.position.x + node.width / 2;
    const centerY = node.position.y + node.height / 2;
    return centerX >= group.position.x && centerX <= group.position.x + group.width && centerY >= group.position.y && centerY <= group.position.y + group.height;
}

/** 拖动结束时找节点应落入的组（中心点判定）；被拖动的组自身不参与，取数组靠后的组（渲染在上层优先）。 */
export function findGroupDropTarget(movedIds: Set<string>, nodes: CanvasNodeData[]) {
    if (nodes.some((node) => movedIds.has(node.id) && node.type === CanvasNodeType.Group)) return null;
    const movingNodes = nodes.filter((node) => movedIds.has(node.id) && node.type !== CanvasNodeType.Group);
    if (!movingNodes.length) return null;
    // Walk backwards instead of copying and reversing; this runs on every drag frame.
    for (let index = nodes.length - 1; index >= 0; index -= 1) {
        const group = nodes[index];
        if (group.type !== CanvasNodeType.Group || movedIds.has(group.id)) continue;
        if (movingNodes.some((node) => containsCenter(group, node))) return group;
    }
    return null;
}

/**
 * 把被拖动的节点收进组内：整体平移 dx/dy，使成员的包围盒尽量贴合组内边距（pad=24）。
 * 包围盒比组内容区还大时以左上角对齐为准，宁超右下不出左上。
 */
export function snapNodesIntoGroup(movedIds: Set<string>, nodes: CanvasNodeData[], group: CanvasNodeData) {
    const movingNodes = nodes.filter((node) => movedIds.has(node.id) && node.type !== CanvasNodeType.Group);
    if (!movingNodes.length) return nodes;
    const pad = 24;
    const bounds = nodeBounds(movingNodes);
    const left = group.position.x + pad;
    const top = group.position.y + pad;
    const right = group.position.x + group.width - pad;
    const bottom = group.position.y + group.height - pad;
    const dx = bounds.right - bounds.left > right - left ? left - bounds.left : bounds.left < left ? left - bounds.left : bounds.right > right ? right - bounds.right : 0;
    const dy = bounds.bottom - bounds.top > bottom - top ? top - bounds.top : bounds.top < top ? top - bounds.top : bounds.bottom > bottom ? bottom - bounds.bottom : 0;
    return nodes.map((node) => {
        if (!movedIds.has(node.id) || node.type === CanvasNodeType.Group) return node;
        return { ...node, position: { x: node.position.x + dx, y: node.position.y + dy }, metadata: { ...node.metadata, groupId: group.id } };
    });
}

// 组包裹选中节点时生成的组矩形：四周留 24px，顶部多留 52px 给组标题栏。
export const GROUP_WRAP_PADDING = 24;
export const GROUP_WRAP_TOP_PADDING = 52;

function selectedGroupIds(selectedIds: Set<string>, nodes: CanvasNodeData[]) {
    return new Set(nodes.filter((node) => selectedIds.has(node.id) && node.type === CanvasNodeType.Group).map((node) => node.id));
}

export function collectGroupMemberNodes(selectedIds: Set<string>, nodes: CanvasNodeData[]) {
    const groups = selectedGroupIds(selectedIds, nodes);
    return nodes.filter((node) => node.type !== CanvasNodeType.Group && (selectedIds.has(node.id) || (node.metadata?.groupId != null && groups.has(node.metadata.groupId))));
}

export function getGroupWrapRect(members: CanvasNodeData[]) {
    const bounds = nodeBounds(members);
    return {
        x: bounds.left - GROUP_WRAP_PADDING,
        y: bounds.top - GROUP_WRAP_TOP_PADDING,
        width: bounds.right - bounds.left + GROUP_WRAP_PADDING * 2,
        height: bounds.bottom - bounds.top + GROUP_WRAP_TOP_PADDING + GROUP_WRAP_PADDING,
    };
}

export function canGroupSelectedNodes(selectedIds: Set<string>, nodes: CanvasNodeData[]) {
    const members = collectGroupMemberNodes(selectedIds, nodes);
    if (members.length < 2) return false;
    const groupId = members[0].metadata?.groupId;
    return !groupId || members.some((node) => node.metadata?.groupId !== groupId);
}

export function canUngroupSelectedNodes(selectedIds: Set<string>, nodes: CanvasNodeData[]) {
    return nodes.some((node) => selectedIds.has(node.id) && (node.type === CanvasNodeType.Group || Boolean(node.metadata?.groupId)));
}

function emptyGroupIds(nodes: CanvasNodeData[], keepId?: string) {
    const used = new Set(nodes.flatMap((node) => (node.type !== CanvasNodeType.Group && node.metadata?.groupId ? [node.metadata.groupId] : [])));
    return new Set(nodes.filter((node) => node.type === CanvasNodeType.Group && node.id !== keepId && !used.has(node.id)).map((node) => node.id));
}

function withoutRemoved(nodes: CanvasNodeData[], connections: CanvasConnection[], removedIds: Set<string>) {
    return {
        nodes: nodes.filter((node) => !removedIds.has(node.id)),
        connections: connections.filter((connection) => !removedIds.has(connection.fromNodeId) && !removedIds.has(connection.toNodeId)),
    };
}

/**
 * 执行成组：拍平选中的子组、成员写入 groupId、组节点插到首个成员位置（保证渲染顺序）、
 * 清理变空的旧组；返回新 nodes/connections 与新的选中集（只选中组本身）。
 * 成员不足 2 个时返回 null 表示不可成组。
 */
export function applyGroupSelection(selectedIds: Set<string>, nodes: CanvasNodeData[], connections: CanvasConnection[], group: CanvasNodeData) {
    const members = collectGroupMemberNodes(selectedIds, nodes);
    if (members.length < 2) return null;
    const memberIds = new Set(members.map((node) => node.id));
    const flattenedGroupIds = selectedGroupIds(selectedIds, nodes);
    const updated = nodes.filter((node) => !flattenedGroupIds.has(node.id)).map((node) => (memberIds.has(node.id) ? { ...node, metadata: { ...node.metadata, groupId: group.id } } : node));
    const insertAt = updated.findIndex((node) => memberIds.has(node.id));
    const withGroup = insertAt < 0 ? [...updated, group] : [...updated.slice(0, insertAt), group, ...updated.slice(insertAt)];
    const next = withoutRemoved(withGroup, connections, new Set([...flattenedGroupIds, ...emptyGroupIds(withGroup, group.id)]));
    return { ...next, selectedIds: [group.id] };
}

/**
 * 执行解组：移除选中组的 groupId（组节点本身删除），保留原成员选中态，清理变空的组。
 * 没有任何可解组对象时返回 null。
 */
export function applyUngroupSelection(selectedIds: Set<string>, nodes: CanvasNodeData[], connections: CanvasConnection[]) {
    const flattenedGroupIds = selectedGroupIds(selectedIds, nodes);
    if (!flattenedGroupIds.size && !nodes.some((node) => selectedIds.has(node.id) && node.metadata?.groupId)) return null;
    const releasedIds = new Set<string>();
    const updated = nodes
        .filter((node) => !flattenedGroupIds.has(node.id))
        .map((node) => {
            const groupId = node.metadata?.groupId;
            if (!groupId) return node;
            if (!flattenedGroupIds.has(groupId) && !selectedIds.has(node.id)) return node;
            releasedIds.add(node.id);
            return { ...node, metadata: { ...node.metadata, groupId: undefined } };
        });
    const next = withoutRemoved(updated, connections, new Set([...flattenedGroupIds, ...emptyGroupIds(updated)]));
    return { ...next, selectedIds: next.nodes.filter((node) => selectedIds.has(node.id) || releasedIds.has(node.id)).map((node) => node.id) };
}

export function findContainingGroupId(node: CanvasNodeData, nodes: CanvasNodeData[]) {
    // Called once per moved node when a drag ends, so avoid copying the node list each time.
    for (let index = nodes.length - 1; index >= 0; index -= 1) {
        const group = nodes[index];
        if (group.type === CanvasNodeType.Group && group.id !== node.id && containsCenter(group, node)) return group.id;
    }
    return undefined;
}

/** 连线拖拽时另一端的吸附锚点：source 在节点左缘中点、target 在右缘中点。 */
export function getConnectionTargetAnchor(node: CanvasNodeData, current: ConnectionHandle) {
    return {
        x: current.handleType === "source" ? node.position.x : node.position.x + node.width,
        y: node.position.y + node.height / 2,
    };
}

/**
 * 规范化一次连线意图为 from→to：目标优先为 config 节点（config 只能当输入方）、
 * 组节点不可连线、config 之间互斥；无法成线返回 null。
 * @param firstHandleType 先落点的手柄类型，用于判断 config 端方向。
 */
export function normalizeConnection(firstNodeId: string, secondNodeId: string, nodes: CanvasNodeData[], firstHandleType: "source" | "target") {
    const first = nodes.find((node) => node.id === firstNodeId);
    const second = nodes.find((node) => node.id === secondNodeId);
    if (!first || !second || first.id === second.id) return null;
    if (second.type === CanvasNodeType.Group) return null;
    if (first.type === CanvasNodeType.Config && second.type === CanvasNodeType.Config) return null;
    if (second.type === CanvasNodeType.Config) return { fromNodeId: first.id, toNodeId: second.id };
    if (first.type === CanvasNodeType.Config && firstHandleType === "target") return { fromNodeId: second.id, toNodeId: first.id };
    if (first.type === CanvasNodeType.Config) return { fromNodeId: first.id, toNodeId: second.id };
    return { fromNodeId: first.id, toNodeId: second.id };
}
