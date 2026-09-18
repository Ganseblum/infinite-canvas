# 无限画布文档索引

## 项目介绍

- [快速开始](/zh-CN/docs/overview/quick-start)
- [功能介绍](/zh-CN/docs/overview/features)
- [Docker 部署](/zh-CN/docs/overview/docker)
- [第三方提示词来源](/zh-CN/docs/overview/third-party-prompt-repositories)

## 操作手册

- [画布节点操作手册](/zh-CN/docs/canvas/canvas-node-manual)
- [画布快捷键](/zh-CN/docs/canvas/canvas-shortcuts)

## 开发与数据

- [项目结构](/zh-CN/docs/development/project-structure)
- [本地开发](/zh-CN/docs/development/local-development)
- [Fork 部署与共享测试数据](../deploy/README.md)
- [画布数据结构](/zh-CN/docs/development/canvas-data-structure)
- [本地 Codex 连接画布原理](/zh-CN/docs/development/local-codex-canvas)

## 商务合作

- [开源协议](/zh-CN/docs/business/license)
- [商务合作](/zh-CN/docs/business/business)

## 支持与安全

- [漏洞提交](/zh-CN/docs/support/security)
- [赞助支持](/zh-CN/docs/support/sponsor)

## 项目进度

- [更新日志](/zh-CN/docs/progress/changelog)
- [账号体系与后端服务规划](/zh-CN/docs/progress/account-backend-plan)
- [第一期执行计划](/zh-CN/docs/progress/account-backend-phase1)
- [第二期执行计划](/zh-CN/docs/progress/account-backend-phase2)
- [第三期执行计划](/zh-CN/docs/progress/account-backend-phase3)
- [第四期执行计划](/zh-CN/docs/progress/account-backend-phase4)
- [第五期执行计划](/zh-CN/docs/progress/account-backend-phase5)
- [待测试](/zh-CN/docs/progress/pending-test)
- [TODO](/zh-CN/docs/progress/todo)

## 说明

- 画布、素材、生成记录与媒体由服务端保存（MySQL 与媒体卷 / S3），浏览器只保留界面状态、Agent 会话与提示词缓存。
- AI 渠道与模型在服务端配置，渠道密钥加密存库，浏览器不持有任何 API Key。
