# 来源与许可

YunX Server 使用 AGPL-3.0，完整文本见 LICENSE。服务端与网页为 NAS 实现；provider/ 直接移植 YunX-Desktop 的平台网络、解析与仓库代码，保留原作者归属，变更和逐文件原始哈希见 provider/UPSTREAM.json：

- CYQawa / YunX — https://github.com/CYQawa/YunX ，参考提交 `648a16d001d5fc8f61fff75ac5b14d266d44cd12`，AGPL-3.0。
- tidain / YunX-Desktop — https://github.com/tidain/YunX-Desktop ，参考提交 `87ff5d6f34ab25627c214eb005ac1d1c07e2b782`，AGPL-3.0。
- GopeedLab / Gopeed v1.9.3 — https://github.com/GopeedLab/gopeed ，下载引擎，GPL-3.0。启用 HTTP、BT 和 ed2k 协议执行器。
- MDUI 2.1.5 / @mdui/icons 1.0.4 — https://github.com/zdhxiong/mdui ，MIT。
- Vue 3.5.44 — https://github.com/vuejs/core ，MIT。

依赖版本与传递依赖由 go.mod/go.sum、web/package-lock.json、provider/build.gradle.kts、provider/package-lock.json 记录。FFmpeg 由运行环境提供；Dockerfile 使用 Debian 包。

应用主图标与 18 个平台图标直接复制自 CYQawa/YunX，未改像素。主图标与上述 YunX-Desktop 提交的主图标字节一致。文件路径、固定提交链接及 SHA-256 见 [图标来源](docs/icon-sources.md)。原项目图标与平台标识保留各自权利人的归属；此处的引用不表示其为本项目背书。

README 的居中项目头图、徽章、截图、功能、使用、构建、常见问题、致谢和协议结构参考以上两个 YunX 项目；功能说明根据本服务端实现编写。

**云析使用AGPL-3.0开源协议** 如果你使用了云析的代码，未经授权请遵守协议，同样以AGPL-3.0开放源代码

本项目同样使用 AGPL-3.0。分发修改版、提供网络服务时，请按 LICENSE 提供对应源代码与许可声明。源代码地址：https://github.com/xiatian410/YunX-Server 。

新增运行依赖：Kotlin / kotlinx.coroutines、OkHttp / Okio、Playwright Core 使用 Apache-2.0；org.json 使用其随包许可。Java、Chromium、Node 和 FFmpeg 由运行环境 / Debian 或基础镜像提供，保留各自许可。provider fat JAR 保留依赖 META-INF 信息；源码构建过程和版本均在仓库公开。

internal/server/motion_photo.go 的 Motion Photo XMP/Exif 容器格式参考同一 YunX-Desktop 提交的 MotionPhotoWriter.kt（AGPL-3.0），以 Go 重写流式封装；原文件 SHA-256 和位置见 provider/UPSTREAM.json 的 references。

网页主题 `web/src/theme.css` 将 CYQawa/YunX 同一固定提交的 `ui/theme/Color.kt` 默认浅/深色角色改写为 MDUI CSS 变量，保留 Copyright (C) 2026 CYQawa / AGPL-3.0 归属。网页导航、字体层级、分组、解析表单和下载卡片依据安卓版源码适配；原文件位置、固定提交链接与 SHA-256 见 [界面来源](docs/design/md3/reference.json)。
