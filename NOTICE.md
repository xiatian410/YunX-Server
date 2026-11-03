# 来源与许可

YunX Server 使用 AGPL-3.0，完整文本见 LICENSE。服务端与网页是为 NAS 重写的独立实现；平台协议与签名实现参考以下项目，保留原作者归属：

- CYQawa / YunX — https://github.com/CYQawa/YunX ，参考提交 `648a16d001d5fc8f61fff75ac5b14d266d44cd12`，AGPL-3.0。
- tidain / YunX-Desktop — https://github.com/tidain/YunX-Desktop ，参考提交 `87ff5d6f34ab25627c214eb005ac1d1c07e2b782`，AGPL-3.0。
- GopeedLab / Gopeed v1.9.3 — https://github.com/GopeedLab/gopeed ，下载引擎，GPL-3.0。只注册 HTTP 协议执行器。
- MDUI 2.1.5 / @mdui/icons 1.0.4 — https://github.com/zdhxiong/mdui ，MIT。
- Vue 3.5.44 — https://github.com/vuejs/core ，MIT。

依赖版本与传递依赖由 go.mod/go.sum、web/package-lock.json 记录。FFmpeg 由运行环境提供；Dockerfile 使用 Debian 包。

应用主图标与四个平台图标直接复制自 CYQawa/YunX，未改像素。主图标与上述 YunX-Desktop 提交的主图标字节一致。文件路径、固定提交链接及 SHA-256 见 [图标来源](docs/icon-sources.md)。原项目图标与平台标识保留各自权利人的归属；此处的引用不表示其为本项目背书。

README 的居中项目头图、徽章、截图、功能、使用、构建、常见问题、致谢和协议结构参考以上两个 YunX 项目；功能说明根据本服务端实现编写。

**云析使用AGPL-3.0开源协议** 如果你使用了云析的代码，未经授权请遵守协议，同样以AGPL-3.0开放源代码

本项目同样使用 AGPL-3.0。分发修改版、提供网络服务时，请按 LICENSE 提供对应源代码与许可声明。源代码地址：https://github.com/xiatian410/YunX-Server 。
