# 划线评论面板外浮布局 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 微博卡片满宽 1000px，划线评论面板用绝对定位浮在容器右侧外；窄屏下面板隐藏，点击高亮文字弹出悬浮评论框。

**Architecture:** 纯前端改动（`weibospider/static/index.html`），CSS 负责定位变化 + 媒体查询，JS 负责窄屏弹出/关闭逻辑 + resize 状态清理。无后端改动。

**Tech Stack:** 原生 HTML/CSS/JS（无框架），Python `pytest` 做前端字符串断言测试

---

## File Structure

- Modify: `weibospider/static/index.html` — CSS 样式区（行 127-180）+ JS 高亮点击逻辑（行 1095、1100-1107）
- Test: `tests/test_frontend.py` — 新增字符串断言测试

---

### Task 1: CSS 宽屏布局改造

**Files:**
- Modify: `weibospider/static/index.html:128-133`
- Test: `tests/test_frontend.py`

- [ ] **Step 1: Write the failing test**

在 `tests/test_frontend.py` 末尾添加：

```python
def test_tweet_row_uses_relative_positioning():
    assert '.tweet-row { position: relative; }' in INDEX_HTML


def test_tweet_card_has_no_max_width():
    assert 'max-width: 680px' not in INDEX_HTML


def test_annotation_panel_absolute_positioned():
    assert 'position: absolute' in INDEX_HTML
    assert 'left: 100%' in INDEX_HTML


def test_annotation_panel_max_height():
    assert 'max-height: 400px' in INDEX_HTML


def test_empty_annotation_panel_hidden():
    assert '.annotation-panel:empty { display: none; }' in INDEX_HTML
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/test_frontend.py::test_tweet_row_uses_relative_positioning tests/test_frontend.py::test_tweet_card_has_no_max_width tests/test_frontend.py::test_annotation_panel_absolute_positioned tests/test_frontend.py::test_annotation_panel_max_height tests/test_frontend.py::test_empty_annotation_panel_hidden -v`
Expected: FAIL

- [ ] **Step 3: Modify CSS**

在 `weibospider/static/index.html` 中，将：

```css
/* Tweet row (card + annotation panel side by side) */
.tweet-row { display: flex; gap: 16px; align-items: flex-start; }
.tweet-row + .tweet-row { margin-top: 10px; }
.tweet-row .card { flex: 1; max-width: 680px; }

/* Annotation panel (right column) */
.annotation-panel { width: 260px; flex-shrink: 0; }
```

替换为：

```css
/* Tweet row (card full-width, annotation panel floats outside) */
.tweet-row { position: relative; }
.tweet-row + .tweet-row { margin-top: 10px; }

/* Annotation panel (floating outside container on wide screens) */
.annotation-panel {
  position: absolute; top: 0; left: 100%; margin-left: 16px;
  width: 260px; max-height: 400px; overflow-y: auto;
}
.annotation-panel:empty { display: none; }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/test_frontend.py::test_tweet_row_uses_relative_positioning tests/test_frontend.py::test_tweet_card_has_no_max_width tests/test_frontend.py::test_annotation_panel_absolute_positioned tests/test_frontend.py::test_annotation_panel_max_height tests/test_frontend.py::test_empty_annotation_panel_hidden -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add weibospider/static/index.html tests/test_frontend.py
git commit -m "feat: annotation panel floats outside container on wide screens"
```

---

### Task 2: CSS 窄屏媒体查询 + 弹出框样式

**Files:**
- Modify: `weibospider/static/index.html` — 在 `.annotation-panel:empty` 规则后添加媒体查询
- Test: `tests/test_frontend.py`

- [ ] **Step 1: Write the failing test**

在 `tests/test_frontend.py` 末尾添加：

```python
def test_narrow_screen_hides_panel_by_default():
    assert '@media (max-width: 1399px)' in INDEX_HTML
    assert '.annotation-panel { display: none; }' in INDEX_HTML


def test_popover_open_class_exists():
    assert '.annotation-panel.popover-open' in INDEX_HTML
    assert 'z-index: 100' in INDEX_HTML
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/test_frontend.py::test_narrow_screen_hides_panel_by_default tests/test_frontend.py::test_popover_open_class_exists -v`
Expected: FAIL

- [ ] **Step 3: Add media query CSS**

在 `weibospider/static/index.html` 中，找到 `.annotation-panel:empty { display: none; }` 这一行，在其后添加：

```css

/* Narrow screen: panel hidden, shown as popover on highlight click */
@media (max-width: 1399px) {
  .annotation-panel { display: none; }
  .annotation-panel.popover-open {
    display: block;
    position: absolute;
    top: auto; bottom: auto;
    left: auto; right: 8px;
    width: 280px;
    max-height: 350px; overflow-y: auto;
    z-index: 100;
    box-shadow: 0 4px 16px rgba(0,0,0,0.15);
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python -m pytest tests/test_frontend.py::test_narrow_screen_hides_panel_by_default tests/test_frontend.py::test_popover_open_class_exists -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add weibospider/static/index.html tests/test_frontend.py
git commit -m "feat: narrow screen popover for annotation panel"
```

---

### Task 3: JS 高亮点击行为（窄屏弹出 vs 宽屏闪烁）

**Files:**
- Modify: `weibospider/static/index.html:1095` — `mark.onclick` 行
- Modify: `weibospider/static/index.html:1100-1107` — `flashAnnotation` 函数
- Test: `tests/test_frontend.py`

- [ ] **Step 1: Write the failing test**

在 `tests/test_frontend.py` 末尾添加：

```python
def test_highlight_click_handles_narrow_screen():
    assert "function isNarrowScreen()" in INDEX_HTML
    assert "matchMedia('(max-width: 1399px)')" in INDEX_HTML


def test_toggle_popover_function_exists():
    assert "function togglePopover(tweetId, annId)" in INDEX_HTML


def test_close_all_popovers_function_exists():
    assert "function closeAllPopovers()" in INDEX_HTML


def test_clear_popover_state_on_resize():
    assert "addEventListener('resize'" in INDEX_HTML
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python -m pytest tests/test_frontend.py::test_highlight_click_handles_narrow_screen tests/test_frontend.py::test_toggle_popover_function_exists tests/test_frontend.py::test_close_all_popovers_function_exists tests/test_frontend.py::test_clear_popover_state_on_resize -v`
Expected: FAIL

- [ ] **Step 3: Modify highlight click handler**

在 `weibospider/static/index.html` 中，找到 `applyHighlights` 函数中的这行：

```javascript
    mark.onclick = () => flashAnnotation(ann.id);
```

替换为：

```javascript
    mark.onclick = () => {
      if (isNarrowScreen()) {
        togglePopover(tweetId, ann.id);
      } else {
        flashAnnotation(ann.id);
      }
    };
```

- [ ] **Step 4: Add helper functions**

在 `flashAnnotation` 函数之前（`applyHighlights` 函数之后），添加：

```javascript

function isNarrowScreen() {
  return window.matchMedia('(max-width: 1399px)').matches;
}

function togglePopover(tweetId, annId) {
  const panel = $('annotation-panel-' + tweetId);
  if (!panel) return;
  if (panel.classList.contains('popover-open')) {
    panel.classList.remove('popover-open');
  } else {
    closeAllPopovers();
    panel.classList.add('popover-open');
    setTimeout(() => flashAnnotation(annId), 50);
  }
}

function closeAllPopovers() {
  document.querySelectorAll('.annotation-panel.popover-open').forEach(p => {
    p.classList.remove('popover-open');
  });
}
```

- [ ] **Step 5: Add document click listener and resize handler**

在 `closeAllPopovers` 函数之后，添加：

```javascript

document.addEventListener('click', (e) => {
  if (!e.target.closest('.annotation-highlight') && !e.target.closest('.annotation-panel')) {
    closeAllPopovers();
  }
});

window.addEventListener('resize', () => {
  if (!isNarrowScreen()) {
    closeAllPopovers();
  }
});
```

- [ ] **Step 6: Run test to verify it passes**

Run: `python -m pytest tests/test_frontend.py::test_highlight_click_handles_narrow_screen tests/test_frontend.py::test_toggle_popover_function_exists tests/test_frontend.py::test_close_all_popovers_function_exists tests/test_frontend.py::test_clear_popover_state_on_resize -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add weibospider/static/index.html tests/test_frontend.py
git commit -m "feat: highlight click toggles popover on narrow screen, flash on wide"
```

---

### Task 4: 全量测试 + 手动验证

**Files:**
- Test: `tests/test_frontend.py`

- [ ] **Step 1: Run all frontend tests**

Run: `python -m pytest tests/test_frontend.py -v`
Expected: All PASS

- [ ] **Step 2: Run full test suite**

Run: `python -m pytest -v`
Expected: All PASS (no regressions)

- [ ] **Step 3: Manual visual verification**

启动应用：`python3 run.py --dev`

宽屏验证（窗口 ≥1400px）：
- 微博卡片与工具栏同宽（1000px）
- 有评论的面板浮在容器右侧外
- 无评论的面板隐藏
- 面板内容过长时出现滚动条（max-height 400px）
- 点击高亮文字 → 面板对应评论闪烁

窄屏验证（窗口 <1400px）：
- 面板默认隐藏
- 点击高亮文字 → 弹出悬浮评论框
- 再次点击同一高亮 → 关闭
- 点击空白区域 → 关闭
- 打开新的弹出框时自动关闭旧的

Resize 验证：
- 从窄屏切到宽屏 → 弹出框状态清除，面板恢复始终可见
- 从宽屏切到窄屏 → 面板隐藏
