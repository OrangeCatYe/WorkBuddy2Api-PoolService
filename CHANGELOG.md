# 变更记录

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [0.2.0] - 2026-10-10

### 新增
- WorkBuddy 平台适配器：迁入原 workbuddy2api-panel 协议层（池、上游、会话粘性、调度、用量、请求归档），原有单测全部保留并通过
- 添加账号：国内版 / 国际版 OAuth 设备授权，凭证 JSON 与 cockpit tools 导出导入
- 账号运维：刷新余额、签到、暂停 / 恢复、解冻、禁用、删除，支持批量
- 积分包明细后台刷新，账号详情按到期时间展示
- 定时任务列表与手动执行
- 网关：响应 `model` 回显客户端请求名、`usage` 采集、模型名积分倍率开关 `credit_in_model_name`
- 面板：添加账号对话框、账号详情抽屉、批量操作栏、任务页、设置页
- Linux 交叉编译与 `scripts/build.sh`

### 变更
- 目录拉取失败时，未知模型返回 503 + `Retry-After`，不再返回 404

## [0.1.0] - 2026-10-10

### 新增
- 核心框架：统一账号模型、Provider 能力接口与注册表、模型目录与名称解析、推理强度映射
- ttswitch 兼容的 `/v1/models` 字段
- 面板骨架：概览、账号、模型目录；明暗主题；管理密钥门禁
- mock 平台
