# 划线评论功能 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在微博列表界面支持选中文本划线高亮、添加评论，评论面板在右侧同行位置展示，随页面一起滚动。

**Architecture:** 新增 `annotations` 表 + 4 个 REST API 端点。前端从单栏改为双栏布局（左微博 + 右评论面板），用 Selection API 计算字符偏移量，用 `<mark>` 标签还原高亮。

**Tech Stack:** Python, Flask, SQLite, Vanilla JS, Selection API

---

### Task 1: 数据库 — annotations 表 + CRUD 方法

**Files:**
- Modify: `weibospider/db.py` (在 `_create_tables` 的第二个 `executescript` 中加表 + 在类末尾加方法)
- Test: `tests/test_db.py`

- [ ] **Step 1: 在 test_db.py 末尾添加 annotation CRUD 测试**

```python
class TestAnnotations:
    def test_create_table(self, db):
        tables = db.conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table'"
        ).fetchall()
        assert 'annotations' in [t[0] for t in tables]

    def test_insert_annotation(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'hello world', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        ann = db.insert_annotation({
            'id': 'a1', 'tweet_id': '1', 'start_offset': 0,
            'end_offset': 5, 'selected_text': 'hello',
            'comment': 'hi', 'field': 'content',
        })
        assert ann['id'] == 'a1'
        assert ann['selected_text'] == 'hello'
        assert ann['comment'] == 'hi'

    def test_get_annotations(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_annotation({
            'id': 'a1', 'tweet_id': '1', 'start_offset': 0,
            'end_offset': 3, 'selected_text': 'hel', 'comment': 'c1', 'field': 'content',
        })
        db.insert_annotation({
            'id': 'a2', 'tweet_id': '1', 'start_offset': 3,
            'end_offset': 5, 'selected_text': 'lo', 'comment': 'c2', 'field': 'content',
        })
        anns = db.get_annotations('1')
        assert len(anns) == 2
        assert anns[0]['comment'] == 'c1'

    def test_update_annotation(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_annotation({
            'id': 'a1', 'tweet_id': '1', 'start_offset': 0,
            'end_offset': 3, 'selected_text': 'hel', 'comment': 'old', 'field': 'content',
        })
        updated = db.update_annotation('a1', 'new comment')
        assert updated['comment'] == 'new comment'
        anns = db.get_annotations('1')
        assert anns[0]['comment'] == 'new comment'

    def test_delete_annotation(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_annotation({
            'id': 'a1', 'tweet_id': '1', 'start_offset': 0,
            'end_offset': 3, 'selected_text': 'hel', 'comment': 'c1', 'field': 'content',
        })
        assert db.delete_annotation('a1') is True
        assert db.delete_annotation('a1') is False
        assert len(db.get_annotations('1')) == 0
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd weibospider && PYTHONPATH=. python3 -m pytest ../tests/test_db.py::TestAnnotations -v`
Expected: FAIL — `AttributeError: 'TweetDB' object has no attribute 'insert_annotation'`

- [ ] **Step 3: 在 db.py `_create_tables` 的第二个 executescript 中添加 annotations 表**

在 `weibospider/db.py` 的 `_create_tables` 方法中，找到第二个 `executescript` 调用（创建 comments / config / 索引的那个），在 `CREATE INDEX IF NOT EXISTS idx_comments_tweet_id ON comments(tweet_id);` 之后、`""")` 之前添加：

```sql
        CREATE TABLE IF NOT EXISTS annotations (
            id          TEXT PRIMARY KEY,
            tweet_id    TEXT NOT NULL,
            start_offset INTEGER NOT NULL,
            end_offset   INTEGER NOT NULL,
            selected_text TEXT NOT NULL,
            comment     TEXT NOT NULL,
            field       TEXT DEFAULT 'content',
            created_at  TEXT,
            updated_at  TEXT,
            FOREIGN KEY (tweet_id) REFERENCES tweets(id)
        );
        CREATE INDEX IF NOT EXISTS idx_annotations_tweet_id ON annotations(tweet_id);
```

- [ ] **Step 4: 在 db.py TweetDB 类末尾（`set_config` 方法之后）添加 annotation CRUD 方法**

```python
    def insert_annotation(self, item):
        with self._lock:
            now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
            self.conn.execute("""
            INSERT INTO annotations
                (id, tweet_id, start_offset, end_offset, selected_text,
                 comment, field, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """, (
                item['id'], item['tweet_id'],
                item['start_offset'], item['end_offset'],
                item['selected_text'], item['comment'],
                item.get('field', 'content'), now, now,
            ))
            self.conn.commit()
            row = self.conn.execute(
                "SELECT * FROM annotations WHERE id=?", (item['id'],)
            ).fetchone()
            return dict(row)

    def get_annotations(self, tweet_id):
        with self._lock:
            rows = self.conn.execute(
                "SELECT * FROM annotations WHERE tweet_id=? ORDER BY start_offset ASC",
                (tweet_id,)
            ).fetchall()
            return [dict(r) for r in rows]

    def update_annotation(self, ann_id, comment):
        with self._lock:
            now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
            cur = self.conn.execute(
                "UPDATE annotations SET comment=?, updated_at=? WHERE id=?",
                (comment, now, ann_id)
            )
            self.conn.commit()
            if cur.rowcount == 0:
                return None
            row = self.conn.execute(
                "SELECT * FROM annotations WHERE id=?", (ann_id,)
            ).fetchone()
            return dict(row)

    def delete_annotation(self, ann_id):
        with self._lock:
            cur = self.conn.execute(
                "DELETE FROM annotations WHERE id=?", (ann_id,)
            )
            self.conn.commit()
            return cur.rowcount > 0
```

- [ ] **Step 5: 运行测试验证通过**

Run: `cd weibospider && PYTHONPATH=. python3 -m pytest ../tests/test_db.py::TestAnnotations -v`
Expected: 5 tests PASS

- [ ] **Step 6: 运行全部测试确认无回归**

Run: `cd weibospider && PYTHONPATH=. python3 -m pytest ../tests/ -v`
Expected: all tests PASS

- [ ] **Step 7: Commit**

```bash
git add weibospider/db.py tests/test_db.py
git commit -m "feat: add annotations table with CRUD methods for highlight comments"
```

---

### Task 2: API — 4 个 annotation 端点

**Files:**
- Modify: `weibospider/app.py` (在现有 tweet API 路由之后添加)
- Test: `tests/test_app.py`

- [ ] **Step 1: 在 test_app.py 的 TestAPI 类中添加 annotation API 测试**

```python
    def _insert_tweet(self, tweet_id='1'):
        import app as app_module
        app_module.DB.insert_tweet({
            '_id': tweet_id, 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'hello world', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })

    def test_get_annotations_empty(self, client):
        self._insert_tweet()
        rv = client.get('/api/tweets/1/annotations')
        assert rv.status_code == 200
        assert json.loads(rv.data) == []

    def test_create_annotation(self, client):
        self._insert_tweet()
        rv = client.post('/api/tweets/1/annotations', json={
            'start_offset': 0, 'end_offset': 5,
            'selected_text': 'hello', 'comment': 'hi', 'field': 'content',
        })
        assert rv.status_code == 201
        data = json.loads(rv.data)
        assert data['comment'] == 'hi'
        assert data['selected_text'] == 'hello'
        assert data['id']

    def test_get_annotations_after_create(self, client):
        self._insert_tweet()
        client.post('/api/tweets/1/annotations', json={
            'start_offset': 0, 'end_offset': 5,
            'selected_text': 'hello', 'comment': 'hi', 'field': 'content',
        })
        rv = client.get('/api/tweets/1/annotations')
        data = json.loads(rv.data)
        assert len(data) == 1
        assert data[0]['comment'] == 'hi'

    def test_update_annotation(self, client):
        self._insert_tweet()
        rv = client.post('/api/tweets/1/annotations', json={
            'start_offset': 0, 'end_offset': 5,
            'selected_text': 'hello', 'comment': 'old', 'field': 'content',
        })
        ann_id = json.loads(rv.data)['id']
        rv = client.put(f'/api/annotations/{ann_id}', json={'comment': 'new'})
        assert rv.status_code == 200
        assert json.loads(rv.data)['comment'] == 'new'

    def test_delete_annotation(self, client):
        self._insert_tweet()
        rv = client.post('/api/tweets/1/annotations', json={
            'start_offset': 0, 'end_offset': 5,
            'selected_text': 'hello', 'comment': 'hi', 'field': 'content',
        })
        ann_id = json.loads(rv.data)['id']
        rv = client.delete(f'/api/annotations/{ann_id}')
        assert rv.status_code == 200
        assert json.loads(rv.data)['deleted'] is True
        rv = client.get('/api/tweets/1/annotations')
        assert json.loads(rv.data) == []

    def test_create_annotation_tweet_not_found(self, client):
        rv = client.post('/api/tweets/999/annotations', json={
            'start_offset': 0, 'end_offset': 1,
            'selected_text': 'x', 'comment': 'x', 'field': 'content',
        })
        assert rv.status_code == 404
```

- [ ] **Step 2: 运行测试验证失败**

Run: `cd weibospider && PYTHONPATH=. python3 -m pytest ../tests/test_app.py -k annotation -v`
Expected: FAIL — 404 for all annotation routes

- [ ] **Step 3: 在 app.py 中添加 4 个 annotation API 端点**

在 `weibospider/app.py` 中找到 `/api/tweets/restore` 路由之后（`api_crawl_comments` 之前），添加以下代码。注意需要在文件顶部添加 `import uuid`（如果还没有的话）。

首先，检查 app.py 顶部是否已 import uuid，如果没有，在 `import json` 之后添加：

```python
import uuid
```

然后在 `/api/tweets/restore` 路由之后添加：

```python
@app.route('/api/tweets/<tweet_id>/annotations')
def api_get_annotations(tweet_id):
    return jsonify(DB.get_annotations(tweet_id))


@app.route('/api/tweets/<tweet_id>/annotations', methods=['POST'])
def api_create_annotation(tweet_id):
    tweet = DB.get_tweet(tweet_id)
    if tweet is None:
        return jsonify({'error': 'tweet not found'}), 404
    data = request.get_json()
    if not data or 'comment' not in data:
        return jsonify({'error': 'missing comment'}), 400
    item = {
        'id': str(uuid.uuid4()),
        'tweet_id': tweet_id,
        'start_offset': data.get('start_offset', 0),
        'end_offset': data.get('end_offset', 0),
        'selected_text': data.get('selected_text', ''),
        'comment': data['comment'],
        'field': data.get('field', 'content'),
    }
    result = DB.insert_annotation(item)
    return jsonify(result), 201


@app.route('/api/annotations/<ann_id>', methods=['PUT'])
def api_update_annotation(ann_id):
    data = request.get_json()
    if not data or 'comment' not in data:
        return jsonify({'error': 'missing comment'}), 400
    result = DB.update_annotation(ann_id, data['comment'])
    if result is None:
        return jsonify({'error': 'annotation not found'}), 404
    return jsonify(result)


@app.route('/api/annotations/<ann_id>', methods=['DELETE'])
def api_delete_annotation(ann_id):
    deleted = DB.delete_annotation(ann_id)
    if not deleted:
        return jsonify({'error': 'annotation not found'}), 404
    return jsonify({'deleted': True})
```

- [ ] **Step 4: 运行测试验证通过**

Run: `cd weibospider && PYTHONPATH=. python3 -m pytest ../tests/test_app.py -k annotation -v`
Expected: 6 tests PASS

- [ ] **Step 5: 运行全部测试确认无回归**

Run: `cd weibospider && PYTHONPATH=. python3 -m pytest ../tests/ -v`
Expected: all tests PASS

- [ ] **Step 6: Commit**

```bash
git add weibospider/app.py tests/test_app.py
git commit -m "feat: add annotation REST API endpoints"
```

---

### Task 3: 前端 — 双栏布局 + CSS

**Files:**
- Modify: `weibospider/static/index.html` (CSS + HTML 结构)

- [ ] **Step 1: 修改 CSS — 加宽页面 + 双栏布局 + 高亮样式**

在 `weibospider/static/index.html` 的 `<style>` 块中：

**1a.** 将 `#app` 最大宽度从 700px 改为 1000px：

找到：
```css
#app { max-width: 700px; margin: 0 auto; padding: 0 12px; }
```
替换为：
```css
#app { max-width: 1000px; margin: 0 auto; padding: 0 12px; }
```

**1b.** 在 `.cookie-warning` CSS 之后（或 `.config-panel` 之前），添加 annotation 相关 CSS：

```css
/* Tweet row (card + annotation panel side by side) */
.tweet-row { display: flex; gap: 16px; align-items: flex-start; }
.tweet-row + .tweet-row { margin-top: 10px; }
.tweet-row .card { flex: 1; max-width: 680px; }

/* Annotation panel (right column) */
.annotation-panel { width: 260px; flex-shrink: 0; }
.annotation-card {
  background: #fff; border: 1px solid #e0e0e0; border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0,0,0,0.06); margin-bottom: 8px; overflow: hidden;
}
.annotation-card-header {
  padding: 8px 12px; border-bottom: 1px solid #eee;
  display: flex; justify-content: space-between; align-items: center;
  font-size: 12px; font-weight: 600; color: #666;
}
.annotation-card-body { padding: 10px 12px; }
.annotation-card-body + .annotation-card-body { border-top: 1px solid #f5f5f5; }
.annotation-selected-text {
  background: #fff3cd; padding: 4px 6px; border-radius: 4px;
  font-size: 12px; color: #666; margin-bottom: 6px; cursor: pointer;
}
.annotation-comment { font-size: 13px; color: #333; line-height: 1.5; word-break: break-word; }
.annotation-actions { margin-top: 6px; font-size: 11px; }
.annotation-actions a { color: #4a9eff; cursor: pointer; margin-right: 8px; }
.annotation-actions a.del { color: #e74c3c; }
.annotation-input {
  background: #fff; border: 1px solid #4a9eff; border-radius: 8px;
  box-shadow: 0 2px 12px rgba(74,158,255,0.15); padding: 12px;
}
.annotation-input textarea {
  width: 100%; border: 1px solid #ddd; border-radius: 6px;
  padding: 8px; font-size: 13px; resize: none; height: 60px; outline: none;
}
.annotation-input textarea:focus { border-color: #4a9eff; }
.annotation-input .actions {
  display: flex; gap: 8px; margin-top: 8px; justify-content: flex-end;
}
.annotation-input .actions button {
  padding: 4px 12px; border-radius: 6px; font-size: 12px; cursor: pointer;
}
.annotation-input .actions .save { background: #4a9eff; color: #fff; border: none; }
.annotation-input .actions .cancel { background: #fff; border: 1px solid #ddd; color: #666; }

/* Highlight marks in tweet content */
.annotation-highlight {
  background: #fff3cd; border-radius: 2px; cursor: pointer; padding: 1px 2px;
}
.annotation-highlight:hover { background: #ffe69c; }
.annotation-card.flash { animation: ann-flash 0.6s ease; }
@keyframes ann-flash {
  0%, 100% { background: #fff; }
  50% { background: #e8f0fe; }
}
```

- [ ] **Step 2: 修改 renderCard 函数 — 改为双栏结构**

在 `weibospider/static/index.html` 中找到 `renderCard` 函数（约 367 行），将整个函数替换为：

```javascript
function renderCard(t) {
  const container = $('cards-container');
  const row = document.createElement('div');
  row.className = 'tweet-row';
  row.dataset.id = t.id;

  const card = document.createElement('div');
  card.className = 'card';
  card.dataset.id = t.id;

  const pics = (typeof t.pic_urls === 'string' ? JSON.parse(t.pic_urls || '[]') : (t.pic_urls || []));
  const comments = t.comments_list || [];
  const commentCount = comments.length;

  card.innerHTML = `
    <input type="checkbox" data-id="${t.id}" onchange="toggleOne(this)" ${selected.has(t.id) ? 'checked' : ''}>
    <div class="card-body">
      <div class="card-meta">
        <span class="card-meta-left">${esc(t.screen_name || '')} &bull; ${esc(t.created_at)} &bull; ${esc(t.source)}</span>
        <span class="card-meta-right">
          <button class="btn-del" onclick="deleteOne('${t.id}')">&times;</button>
        </span>
      </div>
      <div class="card-content" data-field="content" data-tweet-id="${t.id}">${esc(t.content)}</div>
      ${t.retweet_content ? renderRetweet(t) : ''}
      ${pics.length ? `<div class="card-images">${pics.map(p => `<img src="${esc(p)}" loading="lazy">`).join('')}</div>` : ''}
      <div class="card-actions">
        <span>转发 ${t.reposts_count}</span>
        <span>赞 ${t.attitudes_count}</span>
        <span class="comments-toggle" onclick="toggleComments(this, '${t.id}')">评论 ${commentCount}</span>
        <span class="crawl-comments-btn" onclick="crawlComments('${t.id}', this)">抓取评论</span>
      </div>
      <div class="comments-box" id="comments-${t.id}">
        ${comments.length === 0 ? '<div class="comments-box-inner"><div class="comment" style="color:#aaa">暂无评论</div></div>' : ''}
        <div class="comments-box-inner">
          ${comments.length === 0 ? '<div class="comment" style="color:#aaa">暂无评论</div>' : comments.map(c => renderComment(c)).join('')}
        </div>
      </div>
    </div>
  `;

  const panel = document.createElement('div');
  panel.className = 'annotation-panel';
  panel.dataset.tweetId = t.id;
  panel.id = 'annotation-panel-' + t.id;

  row.appendChild(card);
  row.appendChild(panel);
  container.appendChild(row);

  loadAnnotations(t.id);
}
```

- [ ] **Step 3: 修改 renderRetweet — 给转发内容加 data-field**

找到 `renderRetweet` 函数，在转发内容的容器 div 上添加 `data-field="retweet_content"`：

```javascript
function renderRetweet(t) {
  const pics = t.retweet_pic_urls || [];
  return `<div class="retweet-block" data-field="retweet_content" data-tweet-id="${t.id}" style="background:#f0f0f0;border-left:3px solid #ddd;padding:8px 10px;margin:6px 0;border-radius:4px;">
    <span style="color:#e67e22;">@${esc(t.retweet_user||'')}</span>: <span style="font-size:13px;color:#666;">${esc(t.retweet_content)}</span>
    ${pics.length ? '<div class="card-images" style="margin-top:6px;">' + pics.map(p => `<img src="${esc(p)}" loading="lazy">`).join('') + '</div>' : ''}
  </div>`;
}
```

- [ ] **Step 4: 修改 removeCard 函数 — 适配 .tweet-row**

找到 `removeCard` 函数，将选择器从 `.card[data-id=...]` 改为 `.tweet-row[data-id=...]`：

```javascript
function removeCard(id) {
  const row = document.querySelector(`.tweet-row[data-id="${id}"]`);
  if (row) row.remove();
}
```

- [ ] **Step 5: 修改 toggleOne / toggleSelectAll — 适配 .tweet-row**

找到 `toggleOne` 函数中的选择器，将 `.card input[type=checkbox]` 改为 `.tweet-row .card input[type=checkbox]`：

```javascript
function toggleOne(cb) {
  const id = cb.dataset.id;
  cb.checked ? selected.add(id) : selected.delete(id);
  updateBatchBtn();
  $('select-all').checked = selected.size === document.querySelectorAll('.tweet-row .card input[type=checkbox]').length;
}

function toggleSelectAll() {
  const checked = $('select-all').checked;
  document.querySelectorAll('.tweet-row .card input[type=checkbox]').forEach(cb => {
    cb.checked = checked;
    const id = cb.dataset.id;
    checked ? selected.add(id) : selected.delete(id);
  });
  updateBatchBtn();
}
```

- [ ] **Step 6: 验证页面加载正常**

Run: `cd weibospider && python3 -c "import ast; ast.parse(open('app.py').read()); print('OK')"` (确认 Python 无语法错误)

手动启动服务 `python3 run.py --dev`，打开 http://localhost:5000，确认：
- 微博卡片以双栏布局显示（左宽右窄）
- 右栏暂时为空（还没有 annotation JS 逻辑）
- 已有功能（删除、批量删除、全选、切换 tab）正常工作

- [ ] **Step 7: Commit**

```bash
git add weibospider/static/index.html
git commit -m "feat: change to two-column layout with annotation panel placeholder"
```

---

### Task 4: 前端 — 划线交互 + 高亮还原 + 评论面板

**Files:**
- Modify: `weibospider/static/index.html` (JS 逻辑)

- [ ] **Step 1: 添加 mouseup 选区监听 + annotation JS 逻辑**

在 `weibospider/static/index.html` 的 `<script>` 块中，在 `function toast(...)` 之前添加以下所有函数：

```javascript
// ===== Annotation (划线评论) =====

document.addEventListener('mouseup', handleSelection);

function handleSelection(e) {
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || sel.toString().trim().length === 0) return;

  const range = sel.getRangeAt(0);
  const container = range.commonAncestorContainer;
  const fieldEl = findFieldElement(container);
  if (!fieldEl) return;

  const tweetId = fieldEl.dataset.tweetId;
  const field = fieldEl.dataset.field;
  if (!tweetId) return;

  const selectedText = sel.toString().trim();
  if (!selectedText) return;

  const fullText = fieldEl.textContent;
  const startOffset = getOffset(fieldEl, range.startContainer, range.startOffset);
  const endOffset = startOffset + selectedText.length;

  if (fullText.substring(startOffset, endOffset) !== selectedText) return;

  showAnnotationInput(tweetId, field, startOffset, endOffset, selectedText);
  sel.removeAllRanges();
}

function findFieldElement(node) {
  if (node.nodeType === Node.ELEMENT_NODE) {
    if (node.dataset && node.dataset.field) return node;
    return node.closest('[data-field]');
  }
  if (node.parentElement) {
    return node.parentElement.closest('[data-field]');
  }
  return null;
}

function getOffset(fieldEl, startNode, startOffset) {
  const range = document.createRange();
  range.selectNodeContents(fieldEl);
  range.setEnd(startNode, startOffset);
  return range.toString().length;
}

function showAnnotationInput(tweetId, field, startOffset, endOffset, selectedText) {
  const panel = $('annotation-panel-' + tweetId);
  if (!panel) return;

  const existing = panel.querySelector('.annotation-input');
  if (existing) existing.remove();

  const div = document.createElement('div');
  div.className = 'annotation-input';
  div.innerHTML = `
    <div class="annotation-selected-text">"${esc(selectedText)}"</div>
    <textarea placeholder="输入评论..."></textarea>
    <div class="actions">
      <button class="cancel">取消</button>
      <button class="save">保存</button>
    </div>
  `;
  panel.appendChild(div);
  div.querySelector('textarea').focus();

  div.querySelector('.cancel').onclick = () => div.remove();
  div.querySelector('.save').onclick = async () => {
    const comment = div.querySelector('textarea').value.trim();
    if (!comment) { toast('请输入评论', true); return; }
    try {
      const resp = await fetch(`/api/tweets/${tweetId}/annotations`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({ start_offset: startOffset, end_offset: endOffset,
                               selected_text: selectedText, comment, field }),
      });
      const data = await resp.json();
      if (resp.ok) {
        div.remove();
        await loadAnnotations(tweetId);
        toast('已添加划线评论');
      } else {
        toast(data.error || '保存失败', true);
      }
    } catch(e) { toast('请求失败', true); }
  };
}

async function loadAnnotations(tweetId) {
  try {
    const resp = await fetch(`/api/tweets/${tweetId}/annotations`);
    const anns = await resp.json();
    renderAnnotationPanel(tweetId, anns);
    applyHighlights(tweetId, anns);
  } catch(e) {}
}

function renderAnnotationPanel(tweetId, anns) {
  const panel = $('annotation-panel-' + tweetId);
  if (!panel) return;

  const inputBox = panel.querySelector('.annotation-input');
  panel.innerHTML = '';

  if (anns.length === 0) return;

  const card = document.createElement('div');
  card.className = 'annotation-card';
  card.innerHTML = `<div class="annotation-card-header"><span>📝 划线评论</span><span style="font-weight:normal;color:#aaa;">${anns.length} 条</span></div>`;

  anns.forEach(ann => {
    const body = document.createElement('div');
    body.className = 'annotation-card-body';
    body.id = 'ann-body-' + ann.id;
    body.innerHTML = `
      <div class="annotation-selected-text" onclick="scrollToHighlight('${tweetId}', '${ann.id}')">"${esc(ann.selected_text)}"</div>
      <div class="annotation-comment">${esc(ann.comment)}</div>
      <div class="annotation-actions">
        <a onclick="editAnnotation('${ann.id}', '${tweetId}')">编辑</a>
        <a class="del" onclick="deleteAnnotation('${ann.id}', '${tweetId}')">删除</a>
      </div>
    `;
    card.appendChild(body);
  });

  panel.appendChild(card);
}

function applyHighlights(tweetId, anns) {
  const row = document.querySelector(`.tweet-row[data-id="${tweetId}"]`);
  if (!row) return;

  const fieldElements = {};
  row.querySelectorAll('[data-field]').forEach(el => {
    fieldElements[el.dataset.field] = el;
  });

  anns.forEach(ann => {
    const el = fieldElements[ann.field];
    if (!el) return;
    const text = el.textContent;
    const sub = text.substring(ann.start_offset, ann.end_offset);
    if (sub !== ann.selected_text) return;

    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    let pos = 0;
    let startNode = null, startOff = 0, endNode = null, endOff = 0;
    while (walker.nextNode()) {
      const node = walker.currentNode;
      const nodeLen = node.textContent.length;
      if (startNode === null && pos + nodeLen > ann.start_offset) {
        startNode = node;
        startOff = ann.start_offset - pos;
      }
      if (startNode !== null && pos + nodeLen >= ann.end_offset) {
        endNode = node;
        endOff = ann.end_offset - pos;
        break;
      }
      pos += nodeLen;
    }
    if (!startNode || !endNode) return;

    const range = document.createRange();
    range.setStart(startNode, startOff);
    range.setEnd(endNode, endOff);

    const mark = document.createElement('mark');
    mark.className = 'annotation-highlight';
    mark.dataset.annotationId = ann.id;
    mark.onclick = () => flashAnnotation(ann.id);
    range.surroundContents(mark);
  });
}

function flashAnnotation(annId) {
  const body = $('ann-body-' + annId);
  if (body) {
    body.classList.add('flash');
    setTimeout(() => body.classList.remove('flash'), 600);
    body.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
  }
}

function scrollToHighlight(tweetId, annId) {
  const mark = document.querySelector(`.tweet-row[data-id="${tweetId}"] mark[data-annotation-id="${annId}"]`);
  if (mark) {
    mark.scrollIntoView({ behavior: 'smooth', block: 'center' });
    mark.style.background = '#ffe69c';
    setTimeout(() => mark.style.background = '', 1000);
  }
}

async function editAnnotation(annId, tweetId) {
  const body = $('ann-body-' + annId);
  if (!body) return;
  const commentDiv = body.querySelector('.annotation-comment');
  const oldComment = commentDiv.textContent;
  const textarea = document.createElement('textarea');
  textarea.value = oldComment;
  textarea.style.cssText = 'width:100%;border:1px solid #4a9eff;border-radius:6px;padding:8px;font-size:13px;resize:none;height:60px;outline:none;';
  commentDiv.replaceWith(textarea);
  textarea.focus();

  const actionsDiv = body.querySelector('.annotation-actions');
  actionsDiv.innerHTML = `
    <a onclick="saveAnnotationEdit('${annId}', '${tweetId}')">保存</a>
    <a class="del" onclick="loadAnnotations('${tweetId}')">取消</a>
  `;
}

async function saveAnnotationEdit(annId, tweetId) {
  const body = $('ann-body-' + annId);
  if (!body) return;
  const textarea = body.querySelector('textarea');
  const comment = textarea.value.trim();
  if (!comment) { toast('请输入评论', true); return; }
  try {
    const resp = await fetch(`/api/annotations/${annId}`, {
      method: 'PUT',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({ comment }),
    });
    if (resp.ok) {
      await loadAnnotations(tweetId);
      toast('已更新');
    } else {
      const data = await resp.json();
      toast(data.error || '更新失败', true);
    }
  } catch(e) { toast('请求失败', true); }
}

async function deleteAnnotation(annId, tweetId) {
  if (!confirm('确认删除这条划线评论？')) return;
  try {
    const resp = await fetch(`/api/annotations/${annId}`, { method: 'DELETE' });
    if (resp.ok) {
      await loadAnnotations(tweetId);
      toast('已删除');
    }
  } catch(e) { toast('删除失败', true); }
}
```

- [ ] **Step 2: 验证 Python 语法无错误**

Run: `cd weibospider && python3 -c "import ast; ast.parse(open('app.py').read()); print('OK')"`

- [ ] **Step 3: 运行全部测试确认无回归**

Run: `cd weibospider && PYTHONPATH=. python3 -m pytest ../tests/ -v`
Expected: all tests PASS

- [ ] **Step 4: 手动验证完整流程**

启动服务 `python3 run.py --dev`，打开 http://localhost:5000：

1. 在微博正文中选中一段文字 → 右侧同行出现输入框
2. 输入评论，点击保存 → 正文文字高亮，右侧显示评论卡片
3. 点击「编辑」→ 评论切换为 textarea → 修改后保存
4. 点击「删除」→ 高亮和评论卡片消失
5. 点击正文高亮文字 → 右侧评论卡片闪烁
6. 刷新页面 → 高亮和评论仍在

- [ ] **Step 5: Commit**

```bash
git add weibospider/static/index.html
git commit -m "feat: implement highlight annotation with selection, comment panel, and persistence"
```

---

### Task 5: 收尾 — API 批量获取 annotations

**Files:**
- Modify: `weibospider/app.py`

微博列表 API `/api/tweets` 返回的每条 tweet 已经附带 `comments_list`。为了减少前端逐条请求 annotations 的开销，在列表 API 中批量附加 annotations。

- [ ] **Step 1: 修改 api_tweets 端点 — 附加 annotations_list**

在 `weibospider/app.py` 中找到 `api_tweets` 函数，将：

```python
    tweets = DB.get_tweets(page=page, per_page=per_page, sort=sort, deleted=deleted, user_id=user_id)
    # Attach comments for each tweet
    for t in tweets:
        comments = DB.get_comments(t['id'], sort='hot')
        t['comments_list'] = comments
    return jsonify(tweets)
```

替换为：

```python
    tweets = DB.get_tweets(page=page, per_page=per_page, sort=sort, deleted=deleted, user_id=user_id)
    # Attach comments and annotations for each tweet
    for t in tweets:
        comments = DB.get_comments(t['id'], sort='hot')
        t['comments_list'] = comments
        t['annotations_list'] = DB.get_annotations(t['id'])
    return jsonify(tweets)
```

- [ ] **Step 2: 修改前端 renderCard — 用已附带的 annotations_list 而非单独请求**

在 `weibospider/static/index.html` 的 `renderCard` 函数末尾，将 `loadAnnotations(t.id);` 替换为：

```javascript
  const anns = t.annotations_list || [];
  renderAnnotationPanel(t.id, anns);
  setTimeout(() => applyHighlights(t.id, anns), 0);
```

- [ ] **Step 3: 运行全部测试**

Run: `cd weibospider && PYTHONPATH=. python3 -m pytest ../tests/ -v`
Expected: all tests PASS

- [ ] **Step 4: Commit**

```bash
git add weibospider/app.py weibospider/static/index.html
git commit -m "perf: batch-load annotations in tweets API to reduce requests"
```
