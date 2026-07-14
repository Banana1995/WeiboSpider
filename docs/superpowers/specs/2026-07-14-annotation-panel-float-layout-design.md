# 划线评论面板外浮布局设计

## 背景

当前微博卡片（`.card`）和划线评论面板（`.annotation-panel`）在 `.tweet-row` 中以 flex 并排布局，面板占 260px 导致微博卡片被压缩到 `max-width: 680px`，比上方工具栏（1000px）窄，视觉不协调。

## 目标

- 微博卡片宽度与工具栏一致（1000px 满宽）
- 划线评论面板浮在 1000px 容器右侧之外的浏览器空白区域
- 每条微博各自带一个面板（保持现有行为，仅位置改变）
- 窄屏（<1400px）下面板隐藏，点击高亮文字弹出悬浮评论框

## 方案：绝对定位浮出

### CSS 布局改造

**当前结构：**
```
.tweet-row (display: flex; gap: 16px; align-items: flex-start)
  ├── .card (flex: 1; max-width: 680px)
  └── .annotation-panel (width: 260px; flex-shrink: 0)
```

**改造后：**
```
.tweet-row (position: relative)
  ├── .card (width: 100%)
  └── .annotation-panel (position: absolute; top: 0; left: 100%; margin-left: 16px; width: 260px; max-height: 400px; overflow-y: auto)
```

**具体 CSS 改动：**
- `.tweet-row`：去掉 `display: flex; gap: 16px; align-items: flex-start`，改为 `position: relative`
- `.tweet-row .card`：去掉 `flex: 1; max-width: 680px`
- `.annotation-panel`：从 `width: 260px; flex-shrink: 0` 改为 `position: absolute; top: 0; left: 100%; margin-left: 16px; width: 260px; max-height: 400px; overflow-y: auto`

### 细节处理

1. **空面板隐藏** — 没有划线评论的面板隐藏，避免空框浮在外面。`renderAnnotationPanel` 在无评论时已执行 `panel.innerHTML = ''`，用 CSS `.annotation-panel:empty { display: none; }` 即可实现
2. **面板限高** — `max-height: 400px; overflow-y: auto`，防止面板过长重叠下一条微博
3. **断点 1400px** — 容器 1000px + 面板 260px + gap 16px ≈ 1276px，留余量用 1400px

### 窄屏模式（<1400px）

**触发条件：** `@media (max-width: 1399px)`

**面板行为：**
- 面板默认隐藏（`display: none`）
- 点击划线高亮文字 → 弹出悬浮评论框（`.popover-open` class）
- 再次点击同一高亮 / 点击面板外部 → 关闭
- 同一时刻只允许一个弹出框打开

**弹出框样式：**
```css
@media (max-width: 1399px) {
  .annotation-panel { display: none; }
  .annotation-panel.popover-open {
    display: block;
    position: absolute;
    top: auto; bottom: auto;
    left: auto; right: 8px;
    width: 280px;
    z-index: 100;
    box-shadow: 0 4px 16px rgba(0,0,0,0.15);
  }
}
```

### 宽屏 vs 窄屏对比

| | 宽屏 (≥1400px) | 窄屏 (<1400px) |
|---|---|---|
| 面板默认状态 | 始终可见 | 隐藏 |
| 触发方式 | 自动展示 | 点击高亮文字 |
| 面板位置 | 容器右侧外 | 卡片右侧浮层 |
| 关闭方式 | 不需要 | 点击外部/再次点击 |

### JS 改动

1. **划线评论显示逻辑** — 检测是否窄屏（`window.matchMedia('(max-width: 1399px)')`），窄屏时点击 `.annotation-highlight` → toggle 面板 `.popover-open` + 填充对应评论；宽屏保持原有行为

2. **点击外部关闭** — 新增 `document` click 事件监听器：点击非高亮文字、非面板内部 → 关闭所有 `.popover-open`

3. **窗口 resize** — 从窄屏切到宽屏时清除 `.popover-open`（面板恢复始终可见）；宽屏切窄屏面板自动隐藏

### 不受影响的功能

- 划线选中文字创建评论（`mouseup` → 弹出输入框）
- 删除评论、编辑评论
- 微博增删查、评论抓取

### 测试要点

- 宽屏：面板浮在右侧外，不重叠下一条微博（max-height 滚动）
- 窄屏：面板隐藏，点击高亮弹出，点击外部关闭
- resize 切换：状态正确清理
- 空面板：宽屏下也隐藏

## 影响范围

仅 `weibospider/static/index.html` 的 CSS 和 JS 部分，无后端改动。
