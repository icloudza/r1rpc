# 贡献指南

感谢你考虑为 r1rpc 做贡献！无论是修 bug、加功能、补文档还是提建议，都很欢迎。

## 开发环境

- **Go** 1.26+
- **MySQL** 5.7 / 8.0
- **Node** 18+（仅在改前端面板 `web/` 时需要；面板会 `go:embed` 进二进制）

## 本地跑起来

```bash
# 方式一：Docker（推荐，连 MySQL 一起起）
docker compose -f deploy/docker-compose.yml up -d --build

# 方式二：源码
cp config.example.yaml config.yaml   # 改 jwt_secret / admin / mysql
go run ./cmd/dbinit                  # 建库建表（可选，server 启动也会做）
go run ./cmd/server
```

详见 [README](README.md) 与 [docs/tutorial.md](docs/tutorial.md)。

## 提交规范

本项目遵循 [Conventional Commits](https://www.conventionalcommits.org/)：

```
feat:     新功能
fix:      修 bug
docs:     文档
refactor: 重构（不改外部行为）
perf:     性能
test:     测试
chore:    杂项 / 构建 / 依赖
```

示例：`fix(ws): 保活与 RPC 数据流解耦，大流量下不再误判离线`

## 提 PR 之前

- [ ] `go vet ./...` 通过
- [ ] `go build ./...` 通过
- [ ] 改了前端 → 在 `web/` 下 `npm run build`（embed 产物 `internal/web/ui/` 一并提交）
- [ ] 一个 PR 只聚焦一件事，描述清楚动机与改动
- [ ] 涉及用户可感知的行为/接口变化时，同步更新文档

打包发布可用一键脚本：`cmd/build-linux.sh`。

## 报告问题

- Bug / 功能请求：请用 [Issue 模板](.github/ISSUE_TEMPLATE/)。
- **安全漏洞请勿开公开 Issue**，按 [SECURITY.md](SECURITY.md) 私密报告。

## 行为准则

参与本项目即表示你同意遵守 [行为准则](CODE_OF_CONDUCT.md)。
