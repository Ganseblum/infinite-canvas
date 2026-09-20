# office-agent：Claude Agent SDK Runtime（Node 20 glibc slim，保证 SDK 子进程兼容）。
# 构建上下文必须是 office-agent/ 目录本身。
FROM node:20-bookworm-slim
WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY tsconfig.json ./
COPY src ./src

# 运行数据（工作区 / transcript / SDK 会话文件）全部落在挂载卷 /data，重启不丢 resume。
# 必须以非 root 运行：SDK 的 --dangerously-skip-permissions 在 root/sudo 下拒绝启动（安全机制）。
# 宿主机挂载目录需 chown 1000:1000（node 用户）。
RUN mkdir -p /data/workspaces /data/transcripts /data/.claude && chown -R node:node /data /app
ENV NODE_ENV=production \
    OFFICE_PORT=8902 \
    OFFICE_WORKSPACE_DIR=/data/workspaces \
    CLAUDE_CONFIG_DIR=/data/.claude
USER node

EXPOSE 8902
CMD ["node", "node_modules/tsx/dist/cli.mjs", "src/index.ts"]
