## 对齐

文件名表头比正文文件名左移 33px。 位置：`web/src/style.css` → `.table-head > span:first-of-type`；downloads；768,1440px。

## 对比

浅色模式列表与面板边线对背景为 1.65:1，重复边线分散文字注意力。 位置：`web/src/style.css` → `.task-panel, .task-row, .activity, .settings-section`；downloads,settings；390,768,1440px。

## 层级

来源/路径和进度数值为 11px，小于其他同行状态文字。 位置：`web/src/style.css` → `.file-meta, .progress-label > .numeric`；downloads；390,768,1440px。

## 间距

MDUI 内部 container 的 height:100% 占满含 helper 的宿主高度，帮助文字挤入下个字段。 位置：`web/src/style.css` → `.settings-fields > mdui-text-field[helper]::part(container)`；settings；390,768,1440px。
