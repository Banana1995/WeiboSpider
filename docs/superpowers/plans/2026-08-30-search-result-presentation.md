# 搜索结果展示与定位实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让搜索结果显示完整内容（关键词高亮）、评论命中可点击定位到评论区上下文，并修好按微博聚合的分页计数。

**Architecture:** 后端 `build_search_sql` 改为按微博 `GROUP BY` + `json_group_array` 聚合命中，`db.search()` 按 `doc_id` 回源表补齐完整文本并用新的 `highlight_all()` 生成全文高亮；前端复用 `renderCard` 渲染卡片、正文用 `content_hl` 高亮、命中列表可点击触发 `locateComment()` 定位到加了 `data-cid` 锚点的评论。

**Tech Stack:** Python 3.9 / SQLite FTS5 trigram / 原生 JS SPA（`static/index.html`）/ pytest

**Spec:** `docs/superpowers/specs/2026-08-30-search-result-presentation-design.md`

---

## 关键现状（实现者必读）

- `weibospider/search.py`：`build_search_sql`（两条路径）、`make_highlight`（截断版，将被删除）、`escape_snippet`（将被删除）、`fts5_quote`（保留）。
- `weibospider/db.py`：`search()` 在 `db.py:924-977`，`_index_*` 在 255-313 行。`search_index.text` 列对 tweet 是 `content+' '+retweet_content` 拼接、对 annotation 是 `comment+' '+selected_text` 拼接（边界丢失，不能直接用于展示）。
- `weibospider/app.py`：`api_search` 在 907-935 行，**已经**调用了 `_attach_annotations(out['results'])`（上一个 fix 加的，无需再改）。
- `weibospider/static/index.html`：
  - `renderCard(t, container)` @806-857，正文渲染在 828 行 `${esc(t.content)}`。
  - `renderComment(c)` @919-941（顶层 `.comment`，**用 `c._id`——这是个 bug，评论对象实际字段是 `id` 不是 `_id`**，导致折叠组 id 全是 `subs-undefined`）；`renderSubCommentInner(s)` @947-953（子评论 `.comment-reply`）。
  - `toggleComments` @859-880、`toggleSubs` @962-968。
  - `renderSearchResults` @2073-2119（现有「按 tweet_id 去重」逻辑要被删掉）、`renderSearchPager` @2121-2133。
  - `SRC_LABEL` @2035、`searchState` @2026、`escapeHtml` @2029。
  - 搜索相关 CSS @262-273（`.search-hit` 等）。
- 评论对象字段是 `id`（不是 `_id`），子评论折叠用 `id-sub` / `id-btn` 属性 + `.sub-hidden` class；子评论 >5 条折叠（@926）。
- 测试：`tests/test_search.py`（584 行）、`tests/test_frontend.py`（`TestSearchUI` @320-377）。
- 生产 SQLite 3.46.1，本地 venv 3.43.2。`snippet()` 在聚合上下文会报错；`json_group_array(json_object(...))` + `GROUP BY` 在两者均可用（已实测）。

---

### Task 1: `highlight_all()` 全文高亮函数

**Files:**
- Modify: `weibospider/search.py`
- Test: `tests/test_search.py`

- [ ] **Step 1: 写失败测试**

在 `tests/test_search.py` 顶部 import 处（第 9 行）加入 `highlight_all`：

```python
from search import fts5_quote, build_search_sql, highlight_all, MIN_MATCH_LEN
```

并在文件末尾追加测试类：

```python
class TestHighlightAll:
    def test_wraps_every_occurrence_in_mark(self):
        out = highlight_all('量子计算很好，量子计算真棒', '量子计算')
        assert out.count('<mark>量子计算</mark>') == 2

    def test_returns_full_text_not_truncated(self):
        text = '开头' + 'A' * 500 + '量子计算' + 'B' * 500 + '结尾'
        out = highlight_all(text, '量子计算')
        assert out.startswith('开头')
        assert out.endswith('结尾')
        assert '<mark>量子计算</mark>' in out

    def test_escapes_html_to_prevent_xss(self):
        out = highlight_all('<script>alert(1)</script>量子内容', '量子')
        assert '<script>' not in out
        assert '&lt;script&gt;' in out
        assert '<mark>量子</mark>' in out

    def test_no_match_returns_escaped_full_text(self):
        out = highlight_all('完全无关的内容', '量子')
        assert out == '完全无关的内容'
        assert '<mark>' not in out

    def test_case_insensitive_for_ascii(self):
        out = highlight_all('Hello World hello', 'hello')
        assert out.count('<mark>hello</mark>') == 2 or out.count('<mark>Hello</mark>') == 1

    def test_empty_query_returns_escaped_full_text(self):
        assert highlight_all('内容', '') == '内容'

    def test_empty_text_returns_empty(self):
        assert highlight_all('', '量子') == ''
        assert highlight_all(None, '量子') == ''
```

- [ ] **Step 2: 运行确认失败**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestHighlightAll -q`
Expected: FAIL（`ImportError: cannot import name 'highlight_all'`）

- [ ] **Step 3: 实现 `highlight_all`**

在 `weibospider/search.py` 的 `make_highlight` 之前插入：

```python
def highlight_all(text, q):
    """Return `text` fully HTML-escaped with every occurrence of `q` in <mark>.

    Unlike make_highlight(), this does NOT truncate: the whole text comes
    back (escaped) with all matches wrapped. Escaping happens first, then
    <mark> is inserted around each match, so markup in the source text can
    never inject HTML.
    """
    text = text or ''
    if not text:
        return ''
    escaped = html.escape(text)
    if not q:
        return escaped
    ql = html.escape(q).lower()
    out = []
    i = 0
    lower = escaped.lower()
    while True:
        idx = lower.find(ql, i)
        if idx < 0:
            out.append(escaped[i:])
            break
        out.append(escaped[i:idx])
        out.append('<mark>')
        out.append(escaped[idx:idx + len(ql)])
        out.append('</mark>')
        i = idx + len(ql)
    return ''.join(out)
```

- [ ] **Step 4: 运行确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestHighlightAll -q`
Expected: PASS（7 passed）

- [ ] **Step 5: 提交**

```bash
git add weibospider/search.py tests/test_search.py
git commit -m "feat: add highlight_all() full-text highlight helper"
```

---

### Task 2: 重写 `build_search_sql` 为按微博聚合

**Files:**
- Modify: `weibospider/search.py`（`build_search_sql` @25-105）
- Test: `tests/test_search.py`（`TestBuildSearchSql` @142-188）

- [ ] **Step 1: 更新 `TestBuildSearchSql` 断言到新 SQL 形态**

修改 `tests/test_search.py` 中的 `TestBuildSearchSql`（第 142-188 行）。将 `test_long_keyword_uses_match`（第 143-147 行）改为：

```python
    def test_long_keyword_uses_match(self):
        sql, params = build_search_sql('量子计算', page=1, per_page=20)
        assert 'search_index MATCH ?' in sql
        assert 'json_group_array' in sql
        assert 'GROUP BY t.id' in sql
        assert 'snippet(' not in sql
        assert params[0] == '"量子计算"'
```

`test_short_keyword_uses_like_on_source_tables`（第 149-153 行）改为（`matched_text` 已删）：

```python
    def test_short_keyword_uses_like_on_source_tables(self):
        sql, params = build_search_sql('量子', page=1, per_page=20)
        assert 'MATCH' not in sql
        assert 'UNION ALL' in sql
        assert 'json_group_array' in sql
        assert 'GROUP BY t.id' in sql
        assert '%量子%' in params
```

其余测试（`test_match_left_operand_is_table_not_column`、`test_never_orders_by_bm25`、`test_excludes_deleted_tweets`、`test_source_type_filter`、`test_pagination_params_are_last`、`test_date_range_filter`）保持不变——它们断言的内容在新 SQL 中仍然成立（`created_at DESC`、`deleted`、`source_type`、`LIMIT ? OFFSET ?` 且 params 末两位是 `[per_page, offset]`）。

- [ ] **Step 2: 运行确认失败**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestBuildSearchSql -q`
Expected: FAIL（`json_group_array` / `GROUP BY t.id` 尚不存在，`snippet(` 仍在）

- [ ] **Step 3: 重写 `build_search_sql`**

将 `weibospider/search.py` 第 25-105 行整个函数替换为：

```python
def build_search_sql(q, page=1, per_page=20, source_type='all',
                     start_date=None, end_date=None):
    """Build (sql, params) for a search query, aggregated per tweet.

    Returns one row per distinct tweet (the full tweets row via t.*), plus:
      - hit_count: number of matching docs for that tweet
      - hits:      JSON array of {"doc_id", "source_type"} via json_group_array

    The full matched text is NOT returned here (search_index.text concatenates
    content+retweet / comment+selected_text, losing field boundaries). The
    caller backfills text from the source tables by doc_id and highlights in
    Python with highlight_all() -- snippet() cannot run in an aggregate
    context, so it is gone entirely.
    """
    page = max(int(page or 1), 1)
    per_page = min(max(int(per_page or 20), 1), 100)
    offset = (page - 1) * per_page

    filters = []
    filter_params = []
    if source_type and source_type != 'all':
        filters.append("s.source_type = ?")
        filter_params.append(source_type)
    if start_date:
        filters.append("t.created_at >= ?")
        filter_params.append(start_date)
    if end_date:
        filters.append("t.created_at <= ?")
        filter_params.append(end_date + ' 23:59:59')
    extra = (' AND ' + ' AND '.join(filters)) if filters else ''

    if len(q) >= MIN_MATCH_LEN:
        # Path A: FTS5 MATCH. Left operand MUST be the table name.
        sql = f"""
        SELECT COUNT(*) AS hit_count,
               json_group_array(json_object(
                   'doc_id', s.doc_id,
                   'source_type', s.source_type
               )) AS hits,
               t.*
          FROM search_index s
          JOIN tweets t ON t.id = s.tweet_id
         WHERE search_index MATCH ?
           AND t.deleted = 0
           {extra}
         GROUP BY t.id
         ORDER BY t.created_at DESC
         LIMIT ? OFFSET ?
        """
        params = [fts5_quote(q)] + filter_params + [per_page, offset]
        return sql, params

    # Path B: short keyword -> LIKE over source tables (never on the FTS5 table).
    esc = q.replace('\\', '\\\\').replace('%', '\\%').replace('_', '\\_')
    like = f'%{esc}%'
    sql = f"""
        SELECT COUNT(*) AS hit_count,
               json_group_array(json_object(
                   'doc_id', s.doc_id,
                   'source_type', s.source_type
               )) AS hits,
               t.*
          FROM (
                SELECT id AS doc_id, 'tweet' AS source_type, id AS tweet_id
                  FROM tweets
                 WHERE content LIKE ? ESCAPE '\\' OR retweet_content LIKE ? ESCAPE '\\'
                UNION ALL
                SELECT id, 'comment', tweet_id
                  FROM comments
                 WHERE content LIKE ? ESCAPE '\\'
                UNION ALL
                SELECT id, 'annotation', tweet_id
                  FROM annotations
                 WHERE comment LIKE ? ESCAPE '\\' OR selected_text LIKE ? ESCAPE '\\'
               ) s
          JOIN tweets t ON t.id = s.tweet_id
         WHERE t.deleted = 0
           {extra}
         GROUP BY t.id
         ORDER BY t.created_at DESC
         LIMIT ? OFFSET ?
    """
    params = [like] * 5 + filter_params + [per_page, offset]
    return sql, params
```

- [ ] **Step 4: 运行确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestBuildSearchSql -q`
Expected: PASS（7 passed）

- [ ] **Step 5: 提交**

```bash
git add weibospider/search.py tests/test_search.py
git commit -m "feat: aggregate search SQL per tweet via GROUP BY + json_group_array"
```

---

### Task 3: 重写 `db.search()` 补齐文本 + 删除 `make_highlight`/`escape_snippet`

**Files:**
- Modify: `weibospider/db.py`（`search()` @924-977，import @9）
- Modify: `weibospider/search.py`（删除 `make_highlight` @108-134、`escape_snippet` @137-150）
- Test: `tests/test_search.py`（`TestDbSearch` @256-359、`TestSearchApiContract` @362-385、删除 `TestMakeHighlight` @191-222、`TestEscapeSnippet` @224-233）

- [ ] **Step 1: 更新/删除测试到新返回结构**

**删除** `TestMakeHighlight`（第 191-222 行）和 `TestEscapeSnippet`（第 224-233 行）两个类（函数已被删除，被 `TestHighlightAll` 取代）。

**重写 `TestDbSearch`**（第 256-359 行）为适配聚合结构。新结构：`results[]` 每项一条微博，含 `id`、`hits[]`（每 hit 有 `source_type`/`doc_id`/`highlight`）、`content_hl`（tweet 命中且正文非空时存在）。替换整个类：

```python
class TestDbSearch:
    def _hits(self, got, tweet_id):
        r = next(x for x in got['results'] if x['id'] == tweet_id)
        return r['hits']

    def test_finds_tweet_content(self, seeded):
        got = seeded.search('量子计算')
        assert any('tweet' in {h['source_type'] for h in x['hits']} and x['id'] == 't1'
                   for x in got['results'])

    def test_finds_retweet_content(self, seeded):
        got = seeded.search('光刻机')
        assert any(x['id'] == 't1' for x in got['results'])

    def test_finds_comment_content(self, seeded):
        got = seeded.search('量子计算')
        t2 = next(x for x in got['results'] if x['id'] == 't2')
        assert any(h['source_type'] == 'comment' and h['doc_id'] == 'c1' for h in t2['hits'])

    def test_finds_annotation_comment(self, seeded):
        got = seeded.search('笔记说量子')
        t2 = next(x for x in got['results'] if x['id'] == 't2')
        assert any(h['source_type'] == 'annotation' and h['doc_id'] == 'a1' for h in t2['hits'])

    def test_finds_annotation_selected_text(self, seeded):
        got = seeded.search('天气不错')
        assert any(x['id'] == 't2' for x in got['results'])

    def test_excludes_deleted_tweets(self, seeded):
        got = seeded.search('量子计算')
        assert all(x['id'] != 't3' for x in got['results'])

    def test_short_keyword_works(self, seeded):
        got = seeded.search('量子')
        assert got['total'] > 0
        assert any(x['id'] == 't1' for x in got['results'])

    def test_aggregates_multiple_hits_per_tweet(self, seeded):
        """t2 matches via comment c1 AND annotation a1 -> one result, 2 hits."""
        got = seeded.search('量子计算')
        t2 = next(x for x in got['results'] if x['id'] == 't2')
        types = [h['source_type'] for h in t2['hits']]
        assert 'comment' in types and 'annotation' in types

    def test_total_counts_distinct_tweets(self, seeded):
        """t2 matches twice (comment + annotation) but counts once."""
        got = seeded.search('量子计算')
        ids = [x['id'] for x in got['results']]
        assert got['total'] == len(ids)

    def test_comment_hit_has_full_content_highlight(self, seeded):
        got = seeded.search('量子计算')
        t2 = next(x for x in got['results'] if x['id'] == 't2')
        ch = next(h for h in t2['hits'] if h['source_type'] == 'comment')
        assert '评论里提到' in ch['highlight']
        assert '<mark>量子计算</mark>' in ch['highlight']

    def test_annotation_hit_has_note_comment_highlight(self, seeded):
        got = seeded.search('笔记说量子')
        t2 = next(x for x in got['results'] if x['id'] == 't2')
        ah = next(h for h in t2['hits'] if h['source_type'] == 'annotation')
        assert '<mark>笔记说量子</mark>' in ah['highlight']
        assert '我的' in ah['highlight']

    def test_short_keyword_comment_highlight(self, seeded):
        """LIKE path (2-char) must also backfill full comment text + highlight."""
        got = seeded.search('量子')
        t2 = next(x for x in got['results'] if x['id'] == 't2')
        ch = next(h for h in t2['hits'] if h['source_type'] == 'comment')
        assert '<mark>量子</mark>' in ch['highlight']
        assert '评论里提到' in ch['highlight']

    def test_tweet_hit_sets_content_hl(self, seeded):
        got = seeded.search('量子计算')
        t1 = next(x for x in got['results'] if x['id'] == 't1')
        assert t1['content_hl'] == '今天<mark>量子计算</mark>有重大突破'

    def test_source_type_filter(self, seeded):
        got = seeded.search('量子计算', source_type='comment')
        assert got['results']
        for x in got['results']:
            assert all(h['source_type'] == 'comment' for h in x['hits'])

    def test_special_chars_do_not_raise(self, seeded):
        for bad in ["it's", '量子"计算', '-负号', 'AND', 'a*', 'col:val', '(x)']:
            got = seeded.search(bad)
            assert 'results' in got

    def test_empty_query_returns_empty(self, seeded):
        got = seeded.search('')
        assert got['results'] == []
        assert got['total'] == 0

    def test_pagination(self, seeded):
        got = seeded.search('量子计算', page=1, per_page=1)
        assert len(got['results']) <= 1
        assert got['page'] == 1
        assert got['per_page'] == 1

    def test_total_reflects_all_matches(self, seeded):
        got = seeded.search('量子计算', page=1, per_page=1)
        assert got['total'] >= 2

    def test_new_write_is_searchable_immediately(self, seeded):
        seeded.insert_tweet(_mk_tweet('t9', '全新内容超导材料研究'))
        got = seeded.search('超导材料')
        assert any(x['id'] == 't9' for x in got['results'])

    def test_annotation_update_is_searchable(self, seeded):
        seeded.update_annotation('a1', '改后的笔记提到石墨烯')
        got = seeded.search('石墨烯')
        assert any(x['id'] == 't2' for x in got['results'])

    def test_annotation_delete_removes_from_index(self, seeded):
        seeded.delete_annotation('a1')
        got = seeded.search('笔记说量子')
        assert all(x['id'] != 't2' for x in got['results'])

    def test_control_chars_in_query_do_not_raise(self, seeded):
        got = seeded.search('a\x00b\x00c')
        assert 'results' in got
        got2 = seeded.search('量子\x00计算')
        assert 'results' in got2
```

**更新 `TestSearchApiContract.test_response_shape`**（第 378-385 行）：

```python
    def test_response_shape(self, seeded):
        got = seeded.search('量子计算')
        assert set(['results', 'total', 'page', 'per_page']).issubset(got.keys())
        if got['results']:
            r = got['results'][0]
            for key in ('id', 'hits', 'content', 'created_at', 'screen_name'):
                assert key in r
            assert isinstance(r['hits'], list)
```

- [ ] **Step 2: 运行确认失败**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py -q`
Expected: 多个 FAIL（`hits` / `content_hl` 尚不存在，`make_highlight`/`escape_snippet` 尚未删但测试已删）

- [ ] **Step 3: 重写 `db.search()`**

替换 `weibospider/db.py` 第 924-977 行的 `search()` 方法为：

```python
    def search(self, q, page=1, per_page=20, source_type='all',
               start_date=None, end_date=None):
        """Search tweets, comments and annotations for `q`, aggregated per tweet.

        Returns {'results': [...], 'total': int, 'page': int, 'per_page': int}.
        Each result is a full tweet dict plus:
          - hits:  list of {'source_type', 'doc_id' (comment/annotation only),
                            'highlight'} -- full matched text with <mark>.
          - content_hl: highlighted full tweet body (tweet hit + non-empty body).
        """
        q = (q or '').strip()
        q = ''.join(ch for ch in q if ch >= '\x20' and ch != '\x7f')
        page = max(int(page or 1), 1)
        per_page = min(max(int(per_page or 20), 1), 100)
        if not q:
            return {'results': [], 'total': 0, 'page': page, 'per_page': per_page}

        sql, params = build_search_sql(
            q, page=page, per_page=per_page, source_type=source_type,
            start_date=start_date, end_date=end_date,
        )
        with self._lock:
            rows = self.conn.execute(sql, params).fetchall()
            count_sql = "SELECT COUNT(*) FROM (" + \
                sql.replace('LIMIT ? OFFSET ?', '') + ")"
            total = self.conn.execute(count_sql, params[:-2]).fetchone()[0]
            # Backfill full matched text by doc_id from the source tables.
            comment_ids, ann_ids = [], []
            for r in rows:
                for h in json.loads(r['hits'] or '[]'):
                    if h.get('source_type') == 'comment':
                        comment_ids.append(h.get('doc_id'))
                    elif h.get('source_type') == 'annotation':
                        ann_ids.append(h.get('doc_id'))
            comment_text, ann_text = {}, {}
            if comment_ids:
                ph = ','.join('?' * len(comment_ids))
                comment_text = dict(self.conn.execute(
                    f"SELECT id, content FROM comments WHERE id IN ({ph})",
                    comment_ids,
                ).fetchall())
            if ann_ids:
                ph = ','.join('?' * len(ann_ids))
                ann_text = dict(self.conn.execute(
                    f"SELECT id, comment FROM annotations WHERE id IN ({ph})",
                    ann_ids,
                ).fetchall())

        _order = {'tweet': 0, 'comment': 1, 'annotation': 2}
        results = []
        for r in rows:
            d = dict(r)
            for _k in ('pic_urls', 'retweet_pic_urls'):
                if isinstance(d.get(_k), str):
                    try:
                        d[_k] = json.loads(d[_k] or '[]')
                    except (ValueError, TypeError):
                        d[_k] = []
            hits = json.loads(d.pop('hits') or '[]')
            new_hits = []
            tweet_hit = None
            for h in hits:
                st = h.get('source_type')
                if st == 'tweet':
                    text = d.get('content') or d.get('retweet_content') or ''
                    tweet_hit = h
                elif st == 'comment':
                    text = comment_text.get(h.get('doc_id'))
                    if text is None:
                        continue
                elif st == 'annotation':
                    text = ann_text.get(h.get('doc_id'))
                    if text is None:
                        continue
                else:
                    text = ''
                h['highlight'] = highlight_all(text, q)
                new_hits.append(h)
            if not new_hits:
                continue
            new_hits.sort(key=lambda h: (_order.get(h['source_type'], 9),
                                         h.get('doc_id') or ''))
            d['hits'] = new_hits
            if tweet_hit is not None and d.get('content'):
                d['content_hl'] = highlight_all(d['content'], q)
            results.append(d)

        return {'results': results, 'total': total,
                'page': page, 'per_page': per_page}
```

- [ ] **Step 4: 更新 import 与删除废弃函数**

`weibospider/db.py` 第 9 行：

```python
from search import build_search_sql, make_highlight, escape_snippet
```

改为：

```python
from search import build_search_sql, highlight_all
```

删除 `weibospider/search.py` 中的 `make_highlight`（第 108-134 行）和 `escape_snippet`（第 137-150 行）两个函数。

- [ ] **Step 5: 运行确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py -q`
Expected: PASS（全部通过，约 60+ tests）

- [ ] **Step 6: 提交**

```bash
git add weibospider/db.py weibospider/search.py tests/test_search.py
git commit -m "feat: backfill full hit text and highlight in db.search()"
```

---

### Task 4: 前端卡片正文高亮 + 命中列表渲染

**Files:**
- Modify: `weibospider/static/index.html`（`renderCard` @828、`renderSearchResults` @2073-2119、CSS @262-273）

- [ ] **Step 1: `renderCard` 正文支持 `content_hl`**

将 `weibospider/static/index.html` 第 828 行：

```javascript
      <div class="card-content" data-field="content" data-tweet-id="${t.id}">${esc(t.content)}</div>
```

改为：

```javascript
      <div class="card-content" data-field="content" data-tweet-id="${t.id}">${t.content_hl ? t.content_hl : esc(t.content)}</div>
```

（`content_hl` 已由后端 `highlight_all` 转义并插入 `<mark>`，直接作 HTML 用是安全的。）

- [ ] **Step 2: 重写 `renderSearchResults`**

将 `weibospider/static/index.html` 第 2073-2119 行整个函数替换为：

```javascript
function renderSearchResults(data) {
  const meta = document.getElementById('search-meta');
  const box = document.getElementById('search-results');
  const results = data.results || [];
  box.innerHTML = '';
  if (!results.length) {
    meta.textContent = '“' + data.query + '” 共 ' + data.total +
                       ' 条微博（' + data.elapsed_ms + 'ms）';
    box.innerHTML = '<div class="search-empty">没有找到匹配内容</div>';
    return;
  }
  meta.textContent = '“' + data.query + '” 共 ' + data.total +
                     ' 条微博（' + data.elapsed_ms + 'ms）';
  results.forEach(r => {
    renderCard(r, box);
    const row = box.lastElementChild;
    const body = row && row.querySelector('.card-body');
    if (!body) return;
    const wrap = document.createElement('div');
    wrap.className = 'search-hits';
    (r.hits || []).forEach(h => {
      const label = SRC_LABEL[h.source_type] || h.source_type;
      const item = document.createElement('div');
      item.className = 'search-hit-item';
      let html = '<span class="search-src ' + escapeHtml(h.source_type) + '">' +
                 escapeHtml(label) + '</span>';
      if (h.source_type !== 'tweet') {
        item.setAttribute('data-doc-id', h.doc_id || '');
        item.setAttribute('data-tweet-id', r.id);
        item.addEventListener('click', function () {
          locateComment(r.id, h.doc_id);
        });
        html += '<span class="search-hl">' + (h.highlight || '') + '</span>';
      }
      item.innerHTML = html;
      wrap.appendChild(item);
    });
    body.insertBefore(wrap, body.firstChild);
  });
  renderSearchPager();
}
```

- [ ] **Step 3: 更新 CSS**

将 `weibospider/static/index.html` 第 273 行的 `.search-hit` 规则：

```css
.search-hit { margin-bottom: 8px; padding-bottom: 8px; border-bottom: 1px dashed #eee; font-size: 13px; }
```

替换为：

```css
.search-hits { margin-bottom: 8px; }
.search-hit-item { font-size: 13px; line-height: 1.6; padding: 4px 0; border-bottom: 1px dashed #eee; }
.search-hit-item[data-doc-id] { cursor: pointer; color: #2b6cb0; }
.search-hit-item[data-doc-id]:hover { background: #f0f7ff; }
.comment-locate-flash { background: #fff3cd; border-radius: 4px; transition: background 2s; }
```

- [ ] **Step 4: 提交**

```bash
git add weibospider/static/index.html
git commit -m "feat: search cards show full highlighted content and per-hit list"
```

---

### Task 5: 评论定位（`data-cid` 锚点 + `locateComment`）

**Files:**
- Modify: `weibospider/static/index.html`（`renderComment` @919-941、`renderSubCommentInner` @947-953、新增 `locateComment`）

- [ ] **Step 1: 修 `collapseId` bug + 加 `data-cid` 锚点**

`renderComment` 第 924 行：

```javascript
  const collapseId = 'subs-' + c._id;
```

改为：

```javascript
  const collapseId = 'subs-' + c.id;
```

`renderComment` 第 936 行：

```javascript
  return `<div class="comment">
```

改为：

```javascript
  return `<div class="comment" data-cid="${c.id}">
```

`renderSubCommentInner` 第 952 行：

```javascript
  return `<div class="comment-reply"><span class="comment-user">${esc(user)}</span><span class="comment-time">${esc(s.created_at||'')}</span>: ${replyLine}${esc(s.content)}${pics ? `<div class="comment-images">${pics}</div>` : ''}</div>`;
```

改为：

```javascript
  return `<div class="comment-reply" data-cid="${s.id}"><span class="comment-user">${esc(user)}</span><span class="comment-time">${esc(s.created_at||'')}</span>: ${replyLine}${esc(s.content)}${pics ? `<div class="comment-images">${pics}</div>` : ''}</div>`;
```

- [ ] **Step 2: 新增 `locateComment` 函数**

在 `renderSearchResults`（约第 2119 行）之后、`renderSearchPager` 之前插入：

```javascript
async function locateComment(tweetId, docId) {
  const box = document.getElementById('comments-' + tweetId);
  if (!box) return;
  const inner = box.querySelector('.comments-box-inner');
  if (!inner) return;
  if (!inner.dataset.loaded) {
    inner.dataset.loaded = '1';
    inner.innerHTML = '<div class="comment" style="color:#aaa">加载中...</div>';
    try {
      const resp = await fetch('/api/tweets/' + tweetId);
      const data = await resp.json();
      const comments = data.comments || [];
      inner.innerHTML = comments.length === 0
        ? '<div class="comment" style="color:#aaa">暂无评论</div>'
        : comments.map(c => renderComment(c)).join('');
    } catch (e) {
      delete inner.dataset.loaded;
      inner.innerHTML = '<div class="comment" style="color:#aaa">加载失败</div>';
      return;
    }
  }
  box.classList.add('open');
  const target = box.querySelector('[data-cid="' + docId + '"]');
  if (!target) return;
  const hiddenWrap = target.closest('.sub-hidden');
  if (hiddenWrap) {
    hiddenWrap.classList.remove('sub-hidden');
    const group = hiddenWrap.getAttribute('id-sub');
    const btn = box.querySelector('.sub-collapse-btn[id-btn="' + group + '"]');
    if (btn) btn.textContent = '收起';
  }
  target.scrollIntoView({ behavior: 'smooth', block: 'center' });
  target.classList.add('comment-locate-flash');
  setTimeout(function () { target.classList.remove('comment-locate-flash'); }, 2000);
}
```

- [ ] **Step 3: 提交**

```bash
git add weibospider/static/index.html
git commit -m "feat: click a comment hit to expand and scroll to it in the thread"
```

---

### Task 6: 更新前端测试

**Files:**
- Modify: `tests/test_frontend.py`（`TestSearchUI` @320-377）

- [ ] **Step 1: 更新 `TestSearchUI` 断言到新结构**

替换 `tests/test_frontend.py` 第 341-358 行的 `test_search_reuses_render_card` 和 `test_search_dedupes_by_tweet_id` 两个测试为：

```python
    def test_search_reuses_render_card(self):
        """Search results must reuse renderCard so styling/notes panel match."""
        start = INDEX_HTML.index('function renderSearchResults(')
        end = INDEX_HTML.index('function renderSearchPager(')
        body = INDEX_HTML[start:end]
        assert 'renderCard(r, box)' in body
        assert '<div class="card"><div class="card-body">' not in body
        assert 'search-hits' in body
        assert 'search-hit-item' in body
        assert 'h.highlight' in body

    def test_search_renders_aggregated_hits(self):
        """Backend aggregates per tweet; frontend must iterate r.hits."""
        start = INDEX_HTML.index('function renderSearchResults(')
        end = INDEX_HTML.index('function renderSearchPager(')
        body = INDEX_HTML[start:end]
        assert '(r.hits || [])' in body
        assert 'h.source_type' in body

    def test_comment_hit_is_clickable_to_locate(self):
        start = INDEX_HTML.index('function renderSearchResults(')
        end = INDEX_HTML.index('function renderSearchPager(')
        body = INDEX_HTML[start:end]
        assert 'locateComment(r.id, h.doc_id)' in body
        assert 'data-doc-id' in body

    def test_locate_comment_function_exists(self):
        assert 'async function locateComment(tweetId, docId)' in INDEX_HTML
        assert "scrollIntoView" in INDEX_HTML

    def test_comment_dom_has_cid_anchor(self):
        assert 'data-cid="${c.id}"' in INDEX_HTML
        assert 'data-cid="${s.id}"' in INDEX_HTML

    def test_collapse_id_uses_comment_id_not_underscore_id(self):
        """Pre-existing bug: c._id is undefined; must be c.id."""
        assert "'subs-' + c._id" not in INDEX_HTML
        assert "'subs-' + c.id" in INDEX_HTML

    def test_card_uses_content_hl_when_present(self):
        assert 't.content_hl ? t.content_hl : esc(t.content)' in INDEX_HTML
```

- [ ] **Step 2: 运行确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py -q`
Expected: PASS

- [ ] **Step 3: 提交**

```bash
git add tests/test_frontend.py
git commit -m "test: update frontend search assertions for aggregated hits and locating"
```

---

### Task 7: 全量验证 + 浏览器实测 + 提交

**Files:** 无（验证步骤）

- [ ] **Step 1: 跑全量测试**

Run: `source venv/bin/activate && python -m pytest tests/ -q 2>&1 | tail -3`
Expected: `298+ passed, 42 errors`——42 errors 是 AGENTS.md 已记录的 venv werkzeug 缺 `__version__` 环境问题（`test_app.py`/`test_integration.py`），与本次改动无关。确认错误数仍为 42，无新增。

- [ ] **Step 2: 浏览器实测（真实生产数据）**

```bash
cd /Users/gaomanyi/WorkSpace/WeiboSpider
mkdir -p /tmp/wbverify && scp -q weibo:/opt/weibospider/data/data.db /tmp/wbverify/data.db
cd weibospider && source ../venv/bin/activate
DB_PATH=/tmp/wbverify/data.db nohup python run.py --port 5099 > /tmp/wbverify/server.log 2>&1 &
sleep 15; curl -s --max-time 20 http://127.0.0.1:5099/api/stats
```

用 chrome-devtools 打开 http://127.0.0.1:5099/，resize 到 1700x900，搜索「统计局」，验证：
1. 卡片正文完整展开，关键词 `<mark>` 高亮（不再是 12-token 片段）。
2. 命中列表在卡片顶部：tweet 命中显示「微博」徽章；comment/annotation 命中显示徽章 + 完整高亮内容。
3. 点击 comment 命中行 → 评论区展开、滚动到该评论、黄底闪烁。
4. 分页器「共 N 条微博」，页数 = `ceil(N/20)`，翻页无重复微博。
5. Console 无 error（除 favicon.ico 404）。

清尾：`pkill -f "run.py --port 5099"; rm -rf /tmp/wbverify`。

- [ ] **Step 3: 提交（如 Step 2 暴露 bug 则先修再提交）**

```bash
git status --short
```

如无遗留改动则说明验证通过；如有修复，`git add` 相关文件并提交描述性 message。

---

## 验证清单（对照 spec）

- spec §4.1 API 结构 → Task 2（SQL）+ Task 3（db.search）产出 `hits[]` + `content_hl` + `total` 去重数。
- spec §4.2 聚合 SQL → Task 2。
- spec §4.3 文本补齐 + `highlight_all` → Task 1 + Task 3。
- spec §4.4 前端渲染 + 评论定位 → Task 4 + Task 5。
- spec §4.5 分页 → Task 2（`total` 去重）使 `renderSearchPager` 自然正确，无需改。
- spec §5 错误处理 → Task 3（`continue` 丢弃缺失 doc 的 hit）+ Task 5（`if (!target) return`）。
- spec §6 测试 → Task 1/2/3/6。
