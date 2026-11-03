# YunX Server 界面约束

当前基线：根据用户「统一按安卓版布局适配」，复刻 YunX Android 的 Material Design 3 页面。Vue 3 + MDUI 2，面向 NAS 的解析、账号、下载、设置四个页面；数据仍由 API / SSE 提供。

- 页面共用单列结构。宽度 ≥ 600px，或高度 < 480px 且横屏时，使用 80px 导航栏；其余使用底部导航。默认进入解析。
- 解析使用三行描边输入、提取码和整行操作按钮；下载采用任务卡片；账号保留原平台图标、添加入口与操作菜单；设置为分组条目和编辑弹窗。
- 70 个浅/深色角色直接映射 Android Color.kt；28/16/14/12px 字级、16px 外圆角、2px 内圆角、3px 组内间隔来自原版。原文件及提交见 `docs/design/md3/reference.json`。
- 保留 NAS 目录、任务筛选与批量操作、凭据字段、清晰度、种子入口、暂停/恢复、预览等功能。不显示接口没有提供的容量数据。
- 原图字节不变，操作图标继续使用本地 MDUI SVG。无运行时字体或图片 CDN，无新增轮询、重试或延时。

## 当前冻结

功能初稿为 `docs/design/md3/draft.css`；Taste 只输出对齐、对比、层级、间距问题及位置。精修限定 A1/C1/C2 三处；三版采用相同 DOM，只改变点名 CSS，最终选择 A。三轮测量及选择依据见 `docs/design/md3/selection.json`，验证层级见 `docs/validation.md`。

新的安卓版布局由用户明确授权，在功能初稿阶段完成。精修阶段没有改变导航、网格或控件顺序。旧冻结链保留：初稿、原图标增量、平台移植增量、安卓版复刻增量；`scripts/check-freeze.py` 同时验证历史、当前草稿 + 点名精修及嵌入资源。

后续只做有位置、有原因的增量修改；本轮新增发现记录到 `docs/design/next-iteration.md`，不顺带修复。

## 先前基线的历史记录

以下记录对应被本次安卓版基线替代的旧页面，文件与哈希继续保留。


本次选择 A。基线哈希见 `docs/design/baseline-sha256.json`，四类问题及位置见 `review.json`，三个候选 CSS 与 `selection.json` 一并保存。完整验证结果见 `docs/validation.md`。

精修没有修改模板、路由或业务行为，只追加 A 的点名 CSS。没有改变网格列定义、DOM 顺序或导航位置。12px 辅助文字在部分窄屏产生一行正常换行，任务列表/其后记录区最多产生 16.5px 的流式位移；这属于点名文字修复的局部内容高度变化，未用固定高度或隐藏内容掩盖。

后续改动必须作为增量任务说明位置和原因，重新检查所影响状态。不要重新生成整页、重排信息架构或覆盖冻结记录。冻结守卫由 `scripts/check-freeze.py` 检查。新增发现只记录在 `docs/design/next-iteration.md`，本轮不顺带修复。

2026-11-03：用户指定复用原项目图标。仅更新登录页、侧栏、网页图标及账号列表已有图标位置；保留初次冻结快照，更新现行清单。来源见 `docs/icon-sources.md`，增量记录见 `docs/design/increments/2026-11-03-original-icons.md`。

2026-11-03：平台移植增量保留四路由、网格和导航。新增功能控件完成后，Taste 仅点名 `.resolve-extras` 和 `.supported-platforms` 的间距，Impeccable 只修改这两处 margin-top。三版各 36 张截图，DOM 同构，无横向溢出；按结构稳定、2/2 修复和最小位移选择 A，现行清单已冻结。弹窗事件冒泡与 SSE 状态覆盖分别作为独立功能修复记录，未修改版式。新发现 D1 仅记录到下一轮。详见 `docs/design/parity/`。
