---
title: Fork 同步、分支与部署约定
description: 当前 Fork 的分支职责、源头同步和部署规则
---

# Fork 同步、分支与部署约定

当前仓库是 `basketikun/infinite-canvas` 的维护 Fork。约定以少量长期分支配合定期同步，避免把源头代码、规划文档和生产版本混在一起。

## 固定远程与分支职责

| 引用 | 职责 |
| --- | --- |
| `upstream` | 原作者源头仓库；`upstream/main` 只作为同步来源，不直接修改。 |
| `origin` | 当前 Fork；用于推送经过审查的代码和部署版本。 |
| `main` | 唯一的长期稳定集成分支，包含最新源头代码和已经接受的本 Fork 改动。 |
| `codex/account-backend-plan` | 当前账号体系与服务端规划、文档优化及后续实现分支。 |

不需要维护很多永久分支。功能分支或同步分支只在确实需要隔离风险时临时创建，完成合并后即可清理。

## 源头同步流程

在工作区干净、当前改动已经提交或另行保存后执行：

```bash
git fetch upstream --prune
git switch main
git merge upstream/main
git push origin main
git switch codex/account-backend-plan
git merge main
git push origin codex/account-backend-plan
```

同步后先查看冲突、上游 `CHANGELOG.md` 和文档导航，再推送远程。源头同步初期通常是快进合并；当 `main` 已经包含本 Fork 自己的代码后，出现普通合并提交是正常情况。

## 分叉度评估（2026-09 核实）

- 上游活跃度高：近半年 395 个提交、近三月 258 个，最近一次提交在 5 天前；期间还完成过 Next.js → Vite + React Router 的整树迁移，说明上游存在大结构性重构的可能。
- 本地 `main` 当前与 `upstream/main` 完全同步（v0.18.0），规划分支仅领先 1 个文档提交——**现在到第一期合入前是同步成本最低的窗口**。
- 上游热点文件与改造计划高度重叠（近三月改动次数：`pages/canvas/project.tsx` 63、`services/api/image.ts` 29、`app-config-modal.tsx` 23、`use-config-store.ts` 21、`services/api/video.ts` 19、`audio.ts` 11、`model-plugin.ts` 8）——第二期要删除、第四期要整体重写的文件正是上游最活跃的文件，整树 merge 的冲突面会随改造进度急剧扩大。
- `origin/main` 处于分叉状态：其上 5 个本地 `main` 没有的提交全部是早期规划文档底稿（无代码），内容已被 `codex/account-backend-plan` 取代。

## 分阶段同步策略

同步方式随改造进度降级，不要一种方式用到底：

| 阶段 | 同步方式 | 原因 |
| --- | --- | --- |
| 现在 ~ 第一期合入前 | 维持上面的整树 merge 流程，固定节奏：每个 Phase 开工前同步一次，Phase 实施中不同步 | 改动集中在 `server/` 与少量新增文件，冲突面小 |
| 第二期合入后（数据层重写） | 分区同步：`components/canvas/`、`plugins/`、`canvas-agent/` 继续 merge；`services/`、`stores/`、`router.tsx`、`pages/config/` 中已重写的路径改为按需 cherry-pick | 这些路径的上游改动在语义上不再适用，整树 merge 只会产生必输的冲突 |
| 第四期合入后（AI 链路重写、用户渠道删除） | 对 `web/src/services`、`use-config-store`、`router.tsx`、`pages/config/`、语言包的渠道段宣告**硬分叉**，不再合并上游改动；上游的模型适配改动只作为服务端 provider 的参考实现 | 用户渠道删除后，上游对 `image.ts` / `video.ts` 的模型支持类改动没有合并意义 |

## 冲突解决手册

按冲突类别处理，不逐文件枚举：

1. **文档与导航**（`CHANGELOG.md`、`docs/`、`meta.json`、`README.md`）：取双方并集，我方条目在前。`docs/content/docs/` 下已迁去「我的规划」的文档保持删除，上游对它们的更新只读不合并。
2. **i18n 语言包**：取双方 key 并集；合并后必须跑 key 对称性检查（第一期的脚本约定）。
3. **我方删除 / 上游修改**（`webdav-sync.ts`、`app-sync.ts`、`model-plugin.ts`、`seedance-video.ts` 等）：保持删除。先 `git log --oneline <merge-base>..upstream/main -- <path>` 看上游改了什么；确属要修的 bug 时把思路移植进对应的服务端实现，不合并上游补丁（已知案例：`app-sync.ts` 的 `cleanupUnusedMedia` 漏扫 bug 在总规划已记录，修法是服务端回收）。
4. **我方重写 / 上游修改**（`image.ts`、`video.ts`、`audio.ts`、`use-config-store.ts`、`project.tsx`、`router.tsx`）：一律取我方版本，再人工读上游该路径的增量日志，把想要的改动手工移植。取舍标准：画布交互与渲染类改动移植；AI 调用、渠道、模型选择类改动跳过。
5. **上游新增文件**：通常自动合并干净，但新功能若调用已删除的 config-store / 直连 API，`bun run typecheck` 会当场暴露——typecheck 就是语义冲突的检测网。暴露后按我方新架构给该功能接线（登录守卫、统一 client、i18n）。
6. **`package.json`**：依赖取并集，我方新增（如 vitest）不丢。
7. **每次同步的完成定义**：`bun run typecheck`、`bun run test`（有 `server/` 代码后加 `go test ./...`）全部通过，浏览器冒烟走通登录与画布打开，才算同步完成；只解决完文本冲突不算。

## origin/main 的收敛（一次性动作）

`origin/main` 上的 5 个提交全部是被取代的规划底稿、无代码，收敛比长期并存更干净：

1. 先把当前工作区的文档重组提交到 `codex/account-backend-plan` 并推送；
2. `git diff origin/main codex/account-backend-plan -- docs 我的规划` 核对无内容丢失；
3. 经用户确认后 `git push --force-with-lease origin main`（以本地 `main`，即 `upstream/main` 为基重置）；
4. 此后 `origin/main` 只跟随本地 `main` 的正常推送，上述「源头同步流程」里的 `git push origin main` 不再被拒。

## 开发与部署规则

账号体系与服务端目前仍是规划，尚未实现。计划中的 Go 服务放在本仓库的 `server/` 目录，由于它会和 `web/` 共用登录态、数据分发、存储和部署决策，没有必要一开始拆成独立仓库。最终形态预计使用 Docker Compose 管理 Web、API、数据库以及对象存储的连接。

生产环境只从经过审查和验证的 `origin/main` 提交，或从该提交创建的版本 tag 部署。不要直接部署 `upstream/main` 或 `codex/account-backend-plan`。当前仓库主要仍是静态 Web 应用，Docker 静态资源路径尚需最终验证，暂不把生产部署描述为完全验证通过。

服务端开发阶段可以使用规划分支做开发或预发布预览；当对应 Phase 的门禁和用户验收全部通过后，再合并到 `main`，然后从经过验证的 `main` 提交或 tag 部署。
