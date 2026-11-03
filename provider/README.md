# 无界面平台模块

直接移植 `tidain/YunX-Desktop` 固定提交的网络 API、分享仓库与视频解析器，使用 AGPL-3.0。逐文件原始哈希、当前哈希与修改状态见 `UPSTREAM.json`。没有 Compose Desktop、JavaFX、JCEF、桌面数据库或原生窗口依赖。

`./gradlew --no-daemon test fatJar` 生成 `build/libs/yunx-provider.jar`；Java 17 运行。Go 通过私有 stdin/stdout 的逐行 JSON 调用，协议版本 1；不对外监听模块接口。账号凭据由 Go 加密保管，每次请求单独传入，模块不打印凭据或平台错误原文。设备 ID 由 Go 持久化提供。

- `Adapter.kt`：10 个网盘、7 个视频/图集平台；识别、目录、取链、账号检查、清晰度、清理。
- `HttpClients.kt`：公网地址检查、禁用系统代理和自动重试、跨主机跳转移除凭据、请求期限、8 MiB 平台响应上限。
- `HeadlessWebView.kt` / `browser.mjs`：按需 Chromium；同时最多一个浏览器，单次至多 40 秒、250 个页面请求、2 MiB 输出。所有浏览器网络走 Go 的认证本机代理，禁用 WebSocket、Service Worker 和浏览器文件下载。保留原抖音页面提取脚本。
- `src/test/`：原平台识别测试，加上游标、文件 ID、缺失字段、账号状态与 Bilibili 分 P 回归。

NAS 改动：替换桌面设备 ID、日志、浏览器与账号存储边界；补足目录分页 / 路径映射、Bilibili 分 P，保留全部媒体轨道；没有校验接口的账号不标为已验证；转存清理由 Go 记录结果，失败可手动重试。原仓库有上限的转存状态查询保留，新增模块总请求期限约 85 秒；不会添加后台账号轮询。

平台接口、会员权限与验证码仍由平台决定。单元测试和本地夹具不是公开平台成功率证明。这里不存放 Cookie、Token 或真实账号测试数据。
