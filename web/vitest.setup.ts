// i18n 模块在导入时读取 localStorage，Node 测试环境没有该全局对象，这里补一个最小实现。
const storage = new Map<string, string>();

Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
        getItem: (key: string) => storage.get(key) ?? null,
        setItem: (key: string, value: string) => void storage.set(key, String(value)),
        removeItem: (key: string) => void storage.delete(key),
        clear: () => storage.clear(),
        key: (index: number) => [...storage.keys()][index] ?? null,
        get length() {
            return storage.size;
        },
    },
});
