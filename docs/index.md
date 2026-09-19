# 优刻（Infinite Canvas）文档索引

## 项目介绍

- [快速开始](/docs/overview/quick-start)
- [功能介绍](/docs/overview/features)
- [Docker 部署](/docs/overview/docker)
- [第三方提示词来源](/docs/overview/third-party-prompt-repositories)
- [Codex App 插件](/docs/overview/codex-app-plugin)

## 操作手册

- [画布节点操作手册](/docs/canvas/canvas-node-manual)
- [画布快捷键](/docs/canvas/canvas-shortcuts)

## 开发与数据

- [项目结构](/docs/development/project-structure)
- [本地开发](/docs/development/local-development)
- [Fork 部署与共享测试数据](../deploy/README.md)
- [画布数据结构](/docs/development/canvas-data-structure)
- [本地 Codex 连接画布原理](/docs/development/local-codex-canvas)

## 商务

- [开源协议](/docs/business/license)
- [商务合作](/docs/business/business)

## 支持与安全

- [漏洞提交](/docs/support/security)
- [赞助支持](/docs/support/sponsor)

## 项目进度

- [更新日志](/docs/progress/changelog)
- [执行状态与续作指南](/docs/progress/execution-status)
- [待验收](/docs/progress/pending-test)
- [待办](/docs/progress/todo)

## 说明

- 画布、素材、生成记录与媒体由服务端保存（MySQL 与媒体卷 / S3），浏览器只保留界面状态、Agent 会话与提示词缓存。
- AI 渠道与模型在服务端配置，渠道密钥加密存库，浏览器不持有任何 API Key。
- 服务器细节记录在 `我的规划/部署方案（测试与正式环境）.md`，不含密码与密钥；环境文件与真实密钥仅存本地。
