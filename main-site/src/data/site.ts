/**
 * 站点内容唯一数据源（个人主站 youc.online）。
 * 改这里 → `npm run build` → 发布 `out/` 即可，组件不动。
 *
 * 占位资源待替换清单见 README.md：Hero 视频、作品封面（picsum）、OG 图、
 * 社媒与下载链接（当前为 `#`）。AI 生成内容按合规要求带 `aigc: true` 角标。
 */

export const site = {
  name: "航 HANG",
  url: "https://youc.online",
  description: "航的个人聚合门户：图像作品、视频短片、Office 三件套模板与博客手记，全部收进这一站。",
  email: "hi@youc.online",
  location: "HANGZHOU — 杭州",
  channels: [
    { label: "BILIBILI", href: "#" },
    { label: "小红书", href: "#" },
    { label: "GITHUB", href: "https://github.com/Ganseblum" },
  ],
};

export type Work = { id: string; title: string; tag: string; seed: string; aigc?: boolean };
export type Film = { id: string; title: string; type: string; year: string; duration: string; seed: string; aigc?: boolean };
export type Template = { app: "ppt" | "word" | "excel"; no: string; title: string; desc: string; downloads: string };
export type Post = { title: string; excerpt: string; date: string; status: "live" | "wip" };

/** 图像作品（首页取前 6，作品页全量） */
export const works: Work[] = [
  { id: "STILL-01", title: "雾中来信", tag: "概念插画", seed: "ic-w01", aigc: true },
  { id: "STILL-02", title: "霓虹站台", tag: "场景概念", seed: "ic-w02", aigc: true },
  { id: "STILL-03", title: "山海不夜城", tag: "国风插画", seed: "ic-w03", aigc: true },
  { id: "STILL-04", title: "雨夜便利店", tag: "氛围小景", seed: "ic-w04", aigc: true },
  { id: "STILL-05", title: "群星坠落时", tag: "科幻海报", seed: "ic-w05", aigc: true },
  { id: "STILL-06", title: "纸上园林", tag: "平面构成", seed: "ic-w06", aigc: true },
  { id: "STILL-07", title: "旧机器的心", tag: "机械设定", seed: "ic-w07", aigc: true },
  { id: "STILL-08", title: "候鸟旅馆", tag: "绘本内页", seed: "ic-w08", aigc: true },
];

/** 视频短片（首页取前 3，短片页全量） */
export const films: Film[] = [
  { id: "FILM-01", title: "城市呼吸", type: "动态短片", year: "2026", duration: "01:24", seed: "ic-f01", aigc: true },
  { id: "FILM-02", title: "一盏灯的独白", type: "实验动画", year: "2026", duration: "02:10", seed: "ic-f02", aigc: true },
  { id: "FILM-03", title: "南方无雨", type: "氛围影片", year: "2025", duration: "00:48", seed: "ic-f03", aigc: true },
  { id: "FILM-04", title: "机器在做梦", type: "概念 PV", year: "2025", duration: "03:02", seed: "ic-f04", aigc: true },
  { id: "FILM-05", title: "纸飞机航线", type: "手书动画", year: "2025", duration: "01:37", seed: "ic-f05" },
  { id: "FILM-06", title: "晚高峰", type: "动态海报", year: "2024", duration: "00:22", seed: "ic-f06", aigc: true },
];

/** Office 三件套模板（首页取前 3，模板页全量） */
export const templates: Template[] = [
  { app: "ppt", no: "014", title: "深夜蓝 · 工作汇报", desc: "暗色放映厅质感，适合项目复盘与年度汇报，图表页齐全。", downloads: "3.2k" },
  { app: "ppt", no: "021", title: "画廊白 · 作品集", desc: "大图排版优先，为设计师与摄影师准备的作品集骨架。", downloads: "1.8k" },
  { app: "word", no: "007", title: "编辑部 · 长文模板", desc: "标题层级与引用样式预置，写方案、写手记都能直接开笔。", downloads: "2.6k" },
  { app: "word", no: "012", title: "极简简历 · 单页", desc: "一页纸信息密度，中英文字体栈已调好，导出 PDF 不跑版。", downloads: "4.1k" },
  { app: "excel", no: "003", title: "自由职业记账本", desc: "收支、项目、发票三张表联动，月度汇总自动出图。", downloads: "5.7k" },
  { app: "excel", no: "009", title: "内容排期表", desc: "多平台发布排期与状态流转，适合一人运营。", downloads: "1.3k" },
];

/** 博客手记（首页取前 3，手记页全量；status: live=可读，wip=起飞前） */
export const posts: Post[] = [
  { title: "把画布搬进浏览器之后", excerpt: "为什么把作品工作流全部搬进自建的无限画布，以及这一年踩过的坑。", date: "2026-08-30", status: "live" },
  { title: "AI 插画的工作流重组", excerpt: "从提示词到成稿：草稿、放大、修边三段式，比一次成型可控得多。", date: "2026-07-14", status: "live" },
  { title: "模板是怎么被做出来的", excerpt: "一套 PPT 模板的母版、版式与主题色设计顺序，做对了少返工一半。", date: "2026-06-02", status: "live" },
  { title: "视频短片的节奏笔记", excerpt: "卡点不是万能的：先定呼吸感，再谈转场。", date: "2026-05-11", status: "wip" },
  { title: "个人站的第二次重建", excerpt: "从动态框架回到纯静态导出，这次把更新成本降到了一条命令。", date: "2026-04-19", status: "wip" },
];

/** 模板页热搜词 */
export const hotWords: string[] = ["年度总结", "工作汇报", "作品集", "简历", "记账本", "排期表"];
