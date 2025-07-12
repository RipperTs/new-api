# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

New API 是基于 One API 进行二次开发的新一代大模型网关与AI资产管理系统，支持多种AI模型和渠道管理。项目采用 Go + React 架构，支持 Docker 部署。

## 核心架构

### 后端 (Go)
- **main.go**: 应用入口，初始化数据库、中间件和路由
- **model/**: 数据模型层，使用 GORM 支持 SQLite、MySQL、PostgreSQL
- **controller/**: 控制器层，处理 HTTP 请求
- **relay/**: 核心中继层，处理各种AI服务提供商的适配
  - `relay/channel/`: 各个AI服务提供商的适配器 (OpenAI, Claude, Gemini, 百度, 阿里等)
  - `relay_adaptor.go`: 统一适配器接口
- **middleware/**: 中间件 (认证、限流、CORS等)
- **service/**: 业务逻辑层
- **common/**: 公共工具函数和配置
- **router/**: 路由配置

### 前端 (React)
- 位于 `web/` 目录
- 使用 Vite 构建，React 18 + Semi Design UI
- 多语言支持 (i18next)

## 常用命令

### 开发和构建
```bash
# 后端开发 (需要在项目根目录)
go run main.go

# 前端开发 (需要在 web/ 目录)
cd web && npm run dev

# 前端构建
cd web && npm run build

# 完整构建 (前端+后端)
make all

# 仅构建前端
make build-frontend
```

### 代码质量
```bash
# 前端代码格式化
cd web && npm run lint:fix

# 前端代码检查
cd web && npm run lint
```

### Docker 部署
```bash
# 使用 docker-compose 部署
docker-compose up -d

# 直接使用 Docker 镜像
docker run --name new-api -d --restart always -p 3000:3000 -e TZ=Asia/Shanghai -v /data:/data calciumion/new-api:latest
```

## 数据库配置

支持三种数据库：
- SQLite (默认): 数据文件位于 `/data` 目录
- MySQL: 通过 `SQL_DSN` 环境变量配置
- PostgreSQL: 通过 `SQL_DSN` 环境变量配置

示例配置：`SQL_DSN="root:123456@tcp(localhost:3306)/oneapi"`

## 重要环境变量

- `SESSION_SECRET`: 多机部署时必须设置
- `REDIS_CONN_STRING`: Redis 连接字符串，用于缓存
- `CRYPTO_SECRET`: 加密密钥，多机部署共用 Redis 时必须设置
- `GIN_MODE`: 设为 "debug" 启用调试模式

## 添加新的AI服务提供商

1. 在 `relay/channel/` 下创建新目录
2. 实现 `adaptor.go`、`constants.go`、`dto.go` 等文件
3. 在 `relay/relay_adaptor.go` 中注册新适配器
4. 在常量文件中添加渠道类型定义

## 关键代码位置

- 渠道管理: `controller/channel.go`, `model/channel.go`
- 用户认证: `middleware/auth.go`, `controller/user.go`
- API中继: `relay/relay-text.go`, `relay/relay-image.go`
- 令牌管理: `controller/token.go`, `model/token.go`
- 日志记录: `controller/log.go`, `model/log.go`

## 前端组件结构

- `src/components/`: 可复用组件
- `src/pages/`: 页面组件
- `src/context/`: React Context 状态管理
- `src/helpers/`: 工具函数和 API 调用
- `src/constants/`: 常量定义

## 测试和调试

- 后端日志: `logs/` 目录下的日志文件
- 前端开发: 使用 `npm run dev` 启动开发服务器，支持热重载
- API测试: 默认端口 3000，管理员账号 root/123456

## 注意事项

- 前端构建结果嵌入到 Go 二进制文件中 (embed.FS)
- 支持渠道重试和负载均衡
- 实现了多种计费模式 (按次数、按token)
- 支持 Midjourney、Suno 等特殊任务类型