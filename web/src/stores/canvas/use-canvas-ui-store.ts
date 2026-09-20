import { create } from "zustand";

/**
 * 画布列表页 UI store：管项目重命名、多选集合与待删除集合，
 * 仅内存态（刷新即失），列表页与批量操作组件读写。
 */
type CanvasUiStore = {
    // 正在重命名的项目 id 与标题草稿；null 表示无重命名进行中。
    editingProjectId: string | null;
    editingProjectTitle: string;
    // 跨页累积的选中项目集合，用于批量删除。
    selectedProjectIds: string[];
    deleteProjectIds: string[];
    startEditingProject: (id: string, title: string) => void;
    setEditingProjectTitle: (title: string) => void;
    stopEditingProject: () => void;
    toggleSelectedProjectId: (id: string, selected: boolean) => void;
    // 选中集合是跨页累积的，翻页或换筛选条件时当前页拿不到这些 id，必须整体清空。
    clearSelectedProjectIds: () => void;
    setDeleteProjectIds: (ids: string[]) => void;
    removeSelectedProjectIds: (ids: string[]) => void;
};

export const useCanvasUiStore = create<CanvasUiStore>((set) => ({
    editingProjectId: null,
    editingProjectTitle: "",
    selectedProjectIds: [],
    deleteProjectIds: [],
    startEditingProject: (editingProjectId, editingProjectTitle) => set({ editingProjectId, editingProjectTitle }),
    setEditingProjectTitle: (editingProjectTitle) => set({ editingProjectTitle }),
    stopEditingProject: () => set({ editingProjectId: null }),
    toggleSelectedProjectId: (id, selected) => set((state) => ({ selectedProjectIds: selected ? [...new Set([...state.selectedProjectIds, id])] : state.selectedProjectIds.filter((item) => item !== id) })),
    clearSelectedProjectIds: () => set({ selectedProjectIds: [] }),
    setDeleteProjectIds: (deleteProjectIds) => set({ deleteProjectIds }),
    removeSelectedProjectIds: (ids) => set((state) => ({ selectedProjectIds: state.selectedProjectIds.filter((id) => !ids.includes(id)) })),
}));
