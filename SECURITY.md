# 安全策略

## 支持的版本

r1rpc 目前以主分支（`main`）跟进安全修复。建议始终部署最新版本。

## 报告漏洞

**请不要通过公开 Issue / PR / Discussion 披露安全漏洞。**

请通过 GitHub 私密漏洞报告提交：

1. 进入仓库 **Security** 标签页
2. 点击 **Report a vulnerability**
3. 描述漏洞、影响范围与复现步骤

我们会尽快确认并处理，修复发布后会在致谢中注明报告者（如你愿意）。

> 维护者：请在仓库 **Settings → Code security and analysis** 中开启
> **Private vulnerability reporting**，以启用上述私密报告入口。

## 部署侧安全提醒

- 生产务必修改 `jwt_secret`、`admin.password`、`mysql.password`，不要使用示例默认值。
- 对外调用建议为分组开启 `apikey` 鉴权；设备 `device_key` 与调用 `api_key` 请妥善保管、定期轮换。
- 面板与接口应置于 HTTPS / 反向代理之后，避免明文暴露。
