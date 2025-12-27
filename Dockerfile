# --- 阶段 1: 前端构建 ---
FROM node:22-alpine AS frontend-builder

WORKDIR /src/frontend

COPY frontend/package*.json ./
RUN npm ci

COPY frontend/ ./
RUN npm run build

# --- 阶段 2: Go 构建阶段 ---
FROM golang:1.22-alpine AS builder

WORKDIR /src

# 安装必要的构建工具
RUN apk add --no-cache git make

# 复制项目必要文件（.dockerignore 会排除不需要的文件）
COPY Makefile VERSION ./
COPY backend-go/ ./backend-go/

# 从前端构建阶段复制构建产物到 backend-go 嵌入目录
RUN mkdir -p backend-go/frontend/dist
COPY --from=frontend-builder /src/frontend/dist ./backend-go/frontend/dist

# 安装 Go 后端依赖（go mod tidy 确保 go.sum 完整）
RUN cd backend-go && go mod tidy && go mod download

# 构建 Go 后端（前端已嵌入）
RUN cd backend-go && make build

# --- 阶段 3: 运行时 ---
FROM alpine:latest AS runtime

WORKDIR /app

# 安装运行时依赖
RUN apk --no-cache add ca-certificates tzdata

# 从构建阶段复制 Go 二进制文件（已内嵌前端资源）
COPY --from=builder /src/dist/claude-proxy-go /app/claude-proxy

# 创建配置目录和日志目录
RUN mkdir -p /app/.config/backups /app/logs

# 设置时区（可选）
ENV TZ=Asia/Shanghai

# 暴露端口
EXPOSE 3000

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:3000/health || exit 1

# 启动命令
CMD ["/app/claude-proxy"]
