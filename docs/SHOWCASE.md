# README 界面素材

README 采用「产品封面 → 软件定位 → 界面导览 → 下载与使用」的顺序，先让读者看懂软件，再介绍配置与技术细节。

## 素材内容

全部素材保存在 `docs/images/`，README 使用仓库内相对路径引用，GitHub 无需外部图片托管即可渲染。

| 文件 | 用途 |
|---|---|
| `cover.png` | 产品封面，真实深色概览与移动端账号界面组合 |
| `flow.svg` | WorkBuddy 账号、PoolService 与编程客户端的接入关系 |
| `overview-light.png` / `overview-dark.png` | 明暗主题概览 |
| `accounts.png` | 账号筛选、额度与批量操作 |
| `credits.png` | 到期日历、消耗预测、选号策略与回放 |
| `gateway.png` | 客户端配置与独立密钥预算 |
| `usage.png` | 用量趋势与模型、账号、密钥、协议拆分 |
| `models.png` | 模型能力目录 |
| `mobile-accounts.png` | 390px 窄屏账号卡片列表 |

PNG 以 1.5 倍设备像素比拍摄，点击 README 中的图可查看高清原图。封面沿用面板的暖纸、深炭与琥珀配色。

## 数据与隐私

- 截图复用当前构建的 React 前端，不重新绘制或虚构产品界面。
- 账号、标签、积分、统计、模型参数全部由 `scripts/readme-assets.mjs` 内的虚构示例提供；不读取 `config.json`、`auths/`、真实用量或任何账号凭证。
- 密钥仅使用 `<YOUR_API_KEY>` 占位符，不是可用密钥。
- 只读示例服务不代理到实际后台，不调用上游；非 GET 请求统一拒绝。
- 日期固定以避免截图每次生成的相对时间变化；模型、倍率、统计和预测不代表实时数据、承诺或性能测评。
- 示例服务只用于生成文档素材，不编入产品，不添加 Provider，不提供运行时演示模式。

## 更新图片

需要 Node.js 22+（内置 WebSocket）以及已安装的 Chrome / Edge。使用现有项目依赖，不需另装截图工具。

在仓库根目录运行：

```powershell
npm --prefix web run build
node scripts/readme-assets.mjs
```

脚本启动 `127.0.0.1:64098` 的独立只读服务，使用隔离的无头浏览器拍摄，检查页面加载和浏览器错误后自动退出。该端口需空闲；若被其他进程占用，脚本会失败，不会结束或替换已有服务。

浏览器配置放在 Git 已忽略的 `data/readme-capture/chrome-profile/`，不会接触日常浏览器的登录状态。找不到浏览器时可以显式指定路径：

```powershell
$env:README_BROWSER = 'C:/Program Files/Google/Chrome/Application/chrome.exe'
node scripts/readme-assets.mjs
```

只查看示例界面而不重新拍摄：

```powershell
node scripts/readme-assets.mjs --serve
```

用浏览器打开 `http://127.0.0.1:64098`；产品封面在 `/_readme/cover`。完成后按 Ctrl+C 结束服务。仅概览、账号、积分中心、接入、用量与模型目录提供展示数据；未展示页面和写操作不在该服务支持范围内。

## 维护检查

1. 涉及界面布局或导航变更后，重新构建并拍摄，检查文字清晰、表格与桌面页面无裁切。
2. 涉及能力变化后，同步 README 图说，不用尚未实现的功能作为宣传点。
3. 确认 README 中所有相对图片路径存在，展示图片和脚本均不包含真实账号或密钥。
4. 运行本地预检并提交 README、展示素材与生成脚本，不等待远端 CI。
