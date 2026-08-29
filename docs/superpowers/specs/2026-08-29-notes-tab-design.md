# 笔记 tab 设计文档

## 概述

在 PS图 tab 右侧新增「笔记」tab，展示所有添加过划线评论的微博内容及其划线评论内容。本质是一个过滤条件：仅展示带有笔记（划线评论）的微博和笔记内容。

## 需求确认

- **位置**：PS图 tab 右侧
- **内容**：所有有划线评论的微博（完整卡片）+ 对应的划线评论内容
- **排序**：按微博时间倒序
- **平台**：全部平台（微博 + 雪球）
- **样式**：复用现有 `renderCard`（微博正文 + 图片 + 划线高亮 + 右侧划线评论面板），与 PS图 tab 一致
- **过滤**：仅展示非删除（deleted=0）且有划线评论的微博

## 数据流

```
前端「笔记」tab → GET /api/notes → DB.get_annotated_tweets()
  → 返回所有非删除且有划线评论的微博（附 annotations_list）
  → 前端复用 renderCard() 渲染（自动带高亮 + 右侧评论面板）
```

## 后端改动

### db.py — 新增 `get_annotated_tweets()` 方法

仿照 `get_ps_tweets`（db.py:403-421）：

```python
def get_annotated_tweets(self):
    """Return non-deleted tweets that have at least one annotation, all platforms, by created_at DESC."""
    with self._lock:
        rows = self.conn.execute(
            "SELECT * FROM tweets WHERE deleted = 0 "
            "AND id IN (SELECT DISTINCT tweet_id FROM annotations) "
            "ORDER BY created_at DESC"
        ).fetchall()
        results = []
        for row in rows:
            d = dict(row)
            d['pic_urls'] = json.loads(d.get('pic_urls', '[]') or '[]')
            d['retweet_pic_urls'] = json.loads(d.get('retweet_pic_urls', '[]') or '[]')
            d['is_retweet'] = bool(d.get('is_retweet'))
            d['deleted'] = bool(d.get('deleted'))
            results.append(d)
        return results
```

### app.py — 新增端点

放在 `api_ps`（app.py:891）之后：

```python
@app.route('/api/notes')
def api_notes():
    """笔记 tab：返回所有有划线评论的非删除微博。"""
    tweets = DB.get_annotated_tweets()
    _attach_annotations(tweets)
    return jsonify(tweets)
```

## 前端改动（index.html）

1. **Tab 按钮**（line 288 后）：`<button class="tab" data-tab="notes">笔记</button>`
2. **`switchTab`**（line 1333）：加 `isNotes = tab === 'notes'`，纳入 `hideBatch`，卡片容器加 `ps-mode`，调用 `loadNotes()`
3. **`loadNotes()`**：仿照 `loadPs()`（line 1362），fetch `/api/notes` → `renderCard(t)`

## 测试

| 文件 | 用例 |
|------|------|
| `tests/test_db.py` | `get_annotated_tweets` 只返回有划线评论的非删除微博、按时间倒序、无评论不返回 |
| `tests/test_app.py` | `/api/notes` 返回带 `annotations_list` 的微博、无划线评论时返回空数组 |
| `tests/test_frontend.py` | 断言「笔记」tab、`loadNotes` 函数、`/api/notes` fetch 存在 |

## 边界情况

1. 微博被删除后不再展示（deleted=0 过滤），划线评论保留在 DB（现有设计），微博还原则重新出现
2. 无任何划线评论 → 显示「暂无笔记」占位
3. 雪球/微博平台的划线评论都展示（不加 platform 过滤）
4. 划线评论里的图片（OSS markdown）经 `parseMarkdownImages` 在评论面板正常展示
5. 笔记 tab 为只读视图：隐藏 checkbox 和删除按钮（复用 ps-mode CSS）
