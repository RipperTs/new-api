# Repository Guidelines

## Project Structure & Module Organization
- Backend (Go): `main.go`, `controller/`(HTTP 控制器), `service/`(业务), `model/`(数据模型/ORM), `router/`(路由), `middleware/`(中间件), `dto/`(请求/响应结构), `common/` `constant/` `setting/` `relay/`(通用与配置)。
- Frontend (React+Vite): `web/`（产物输出到 `web/dist/`）。
- Assets & Docs: `docs/`, `logs/`（运行日志，已被 `.gitignore` 忽略）。

## Build, Test, and Development Commands
- 本地一键启动（前端构建 + 后端运行）：
  ```bash
  make all
  ```
- 构建前端（Vite）：`make build-frontend`；开发模式：`cd web && npm run dev`。
- 启动后端（开发）：`make start-backend` 或 `go run main.go`。
- 构建后端二进制：`go build -o bin/new-api main.go`。
- 运行测试：`go test ./...`（当前测试较少，欢迎补充）。
- Docker 部署请参考 `README.md` 与 `docker-compose.yml`。

## Coding Style & Naming Conventions
- Go：必须 `go fmt ./...`；包名小写；导出标识符用 `PascalCase`，非导出用 `camelCase`；文件名小写下划线；JSON 标签统一 `snake_case`（如 `json:"user_id"`）。
- 前端：使用 Prettier（`npm run lint`/`npm run lint:fix`），单引号；Vite + React 组件遵循函数式写法与 Hooks 规范。
- 提交前确保无编译错误与格式化差异。

## Testing Guidelines
- 框架：Go 原生 `testing`；文件命名 `*_test.go`，与被测包同目录。
- 建议优先为 `service/` 与复杂 `controller/` 增加表驱动测试；运行：`go test -v ./...`，可选覆盖率：`go test -cover ./...`。

## Commit & Pull Request Guidelines
- 提交信息：动词开头、简洁明确（中文为主），必要时可用 Conventional Commits，如 `feat(router): 支持X`。
- PR 要求：
  - 描述变更、理由与影响；关联 Issue。
  - 涉及配置请同步更新 `.env.example` 与文档。
  - 本地验证通过：前端构建/开发可用，后端可编译与运行（`make all` 或等效流程）。
- CI：分支 `jl` push 触发镜像构建（见 `.github/workflows/`）。

## Security & Configuration Tips
- 配置通过环境变量与 `.env` 管理（示例见 `.env.example`）；请勿提交敏感信息，`.env` 已忽略。
- 多机部署需设置 `SESSION_SECRET`，如使用公共 Redis 需设置 `CRYPTO_SECRET`（详见 README）。

## Agent-Specific Instructions
- 遵循本文件约定；最小化改动，避免不相关重构；新增环境变量与公共接口必须补文档与示例。
- 保持目录职责清晰：路由→控制器→服务→模型；公共方法放入 `common/`，常量放入 `constant/`。

