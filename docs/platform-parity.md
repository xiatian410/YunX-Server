# 原版平台移植验收

本轮完成原版注册的 **10 类网盘、7 个视频/图集平台与 GitHub** 在 NAS 服务中的解析、取链与下载接入，同时接入 HTTP/HLS/DASH、磁力/BT/ed2k。原项目的纯网络代码随服务构建为无界面 JVM 模块；Go 管理账号、队列、持久化和 NAS 文件，MDUI 网页沿用四个页面。

原先平台少的原因是服务端只有四个平台分支，并不是接口无数据、字段没有显示或浏览器缓存。本轮扩展了真实适配和执行链路，没有用平台注册表代替实现。

## 逐平台实现

“已接入”表示有解析 → 目录/媒体 → 取链 → 下载路径；最后一列是验收状态，不能读成真实账号测试通过。

| 平台 | 解析 / 列表 | 取链 | 验收 |
| --- | --- | --- | --- |
| 夸克 | QuarkApi + QuarkResolveRepository | 游客 / 登录态 / 转存 | 识别、云盘分页、字段错误夹具通过；真实账号待验 |
| UC | UCApi + UCResolveRepository | 分享直链 / 个人盘 | 识别、接口夹具、字段错误通过；真实账号待验 |
| 123 | Pan123Api + Pan123ResolveRepository | 分享 / Token | 页码与游标、超大文件 ID、协议签名夹具通过；真实账号待验 |
| 百度 | BaiduApi + BaiduResolveRepository | 个人盘 / 转存 locatedownload | 源码、识别、目录路径与清理调用链检查；真实账号待验 |
| 迅雷 | XunleiApi + NAS 绑定 | 个人盘 / 分享转存 | 识别、设备凭据、云盘游标夹具通过；真实账号待验 |
| 139 | C139Api + C139ResolveRepository | 分享 / 个人盘 | 源码、识别、分页与 Authorization 映射检查；真实账号待验 |
| 115 | Pan115Api + Pan115ResolveRepository | App / Web 取链、转存 | 识别与字段错误夹具、转存清理调用链检查；真实账号待验 |
| 光鸭 | GuangYaApi + GuangYaResolveRepository | 分享直链 / 登录转存 | 源码、识别、根目录与设备映射检查；真实账号待验 |
| 蓝奏 | LanzouApi + LanzouResolveRepository | 分享 / 个人盘 | 源码、识别、目录 ID 映射检查；真实账号待验 |
| 蓝奏优享 | ILanzouApi + NAS 绑定 | 分享 / 个人盘 | 源码、识别、UUID 与用户 ID 映射检查；真实账号待验 |
| Bilibili | BilibiliParser | 分 P / 清晰度 / DASH | 分 P CID 与识别夹具、真实本地音视频合轨通过；真实平台待验 |
| 抖音 | DouyinParser + Chromium | 视频 / 图集 / 动态照片 | 识别、受控浏览器与 Motion Photo 封装通过；真实平台待验 |
| 小红书 | XhsParser | 视频 / 图集 | 源码、识别与凭据调用链检查；真实平台待验 |
| 快手 | KuaishouParser | 视频 / 图集 | 源码与识别通过；没有独立登录检查时保持未检查；真实平台待验 |
| 微博 | WeiboParser | 视频 / 图集 / 动态照片 | 源码、识别和 Motion Photo 容器测试通过；真实平台待验 |
| AcFun | AcFunParser | HLS | 源码与识别、本地 AES-128 HLS 合并通过；真实平台待验 |
| Pixiv | PixivParser | 多图 / 原图 | 源码与识别、账号检查错误路径修正；真实平台待验 |
| GitHub | Go GitHub API | 文件 / 归档 / Release | PAT 私有仓库列表、Release 取链夹具通过；真实私有仓库待验 |

上游固定提交、逐文件修改与哈希见 [provider/UPSTREAM.json](../provider/UPSTREAM.json)。其中 62 个原版源码/测试文件直接包含在仓库，18 个为 NAS 边界、分页、目录、清理、分 P 和错误处理作了适配。

## 下载执行与 NAS 行为

- HTTP：真实本地 Range 文件、暂停、源变化、重启恢复、最终哈希、删除确认。
- HLS：最高码率列表、AES-128、KEY/MAP/分片地址重写、本地 FFmpeg 合并；缓存按 ETag 与 SHA-256 校验；拒绝持续直播、DRM、结构或内容变化。
- DASH：真实本地视频与音频合并，FFprobe 验证两个轨道。
- Motion Photo：保留静图和视频，JPEG 可读取，XMP 长度与 Exif 标识测试。
- BT：导入种子、磁力元数据解析、文件选择、相对目录、完成后停止做种。受控本地 webseed 验证选中文件哈希、ZIP 输出、暂停后跨进程恢复及哈希一致。
- ed2k：文件链接校验和 Gopeed 执行路径已接入；公开 ed2k 网络未实测。磁力公开 DHT 与真实 peer 可达性也没有用本地 webseed 测试代替。
- 账号：18 平台输入入口，Cookie/Token 及额外设备字段加密保存，编辑不回显；缺少独立检查接口时不伪报“已验证”。
- 清理：下载后清理本次转存对象，失败保留记录并提供手动重试。百度保留共享临时目录，避免误删并发任务文件。
- 网页：种子上传、视频清晰度、平台说明、本地媒体预览、目录 ZIP；入队后的清晰度固定，不跟随随后页面切换。

## 约束与边界

部署需要 Java 17、Node 22、Chromium、FFmpeg；Dockerfile 已包含这些组件。不是只分发单个 Go 文件就能获得完整能力。JVM 最大堆 192 MiB，模块最多 4 个请求，浏览器同时一个、一次至多 40 秒。没有新增后台轮询。

种子最大 2 MiB / 1000 文件，磁力元数据 45 秒截止；HLS 最多 10000 个资源 / 5 层嵌套。P2P HTTP 流量走受控本机代理，P2P peer/UDP 使用原引擎。HTTP/HLS 限速不作用于 P2P。平台会员、登录、验证码与访问权限照常生效。

原生桌面托盘、通知、内嵌扫码/网页登录窗口采用 NAS 对应交互或不运行；本轮账号统一使用手动凭据。预览针对已完成媒体。平台上传、定时任务、媒体库整理、持续直播与 DRM 不属于这次平台下载迁移。

## 界面验收

Taste 只列出四类问题及位置，发现 S1/S2 两处间距；Impeccable 只修相应 margin-top。三版每版 24 个页面状态 + 12 个新增控件状态，按 DOM 稳定、2/2 修复和最小位移选择 A。原版式与冻结历史保留。新发现的账号弹窗辅助文字高度问题 D1 仅记录待下一轮，未混入精修。

弹窗内部关闭事件冒泡、迟到的 HTTP 状态覆盖 SSE 两项作为独立功能修复，有各自回归记录；未改导航、网格或 DOM 顺序。记录在 [docs/design/parity](design/parity)。
