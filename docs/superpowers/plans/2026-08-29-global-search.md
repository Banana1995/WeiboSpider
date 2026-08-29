# 全局搜索功能 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 WeiboSpider 添加全局搜索，可检索微博正文、转发原文、抓取的评论、划线笔记及被划线原文。

**Architecture:** 新增一张 SQLite FTS5 虚拟表 `search_index`（trigram 分词）聚合三类文本来源，写入业务表时同步维护索引。查询走两条路径：关键词 ≥3 字用 `MATCH`（走 trigram 索引，0.1ms，带 `snippet()` 高亮）；<3 字直接 UNION 查业务表 LIKE（实测比在 FTS5 表上 LIKE 更快）。前端在工具栏加快速搜索框，回车进入独立"搜索"标签页展示结果。

**Tech Stack:** Python 3.9 / SQLite FTS5 trigram（本地 3.43.2、容器 3.46.1，均已验证支持）/ Flask 2.3 / 原生 JS SPA（`static/index.html` 单文件）/ pytest

**设计文档:** `docs/superpowers/specs/2026-08-29-global-search-design.md`

---

## 关键约束（实测得出，务必遵守）

这些是实测验证过的硬约束，违反任何一条都会导致功能不可用：

1. **`MATCH` 的左值必须是表名**，不能是列名。`WHERE search_index.text MATCH ?` 报错，必须写 `WHERE search_index MATCH ?`
2. **`MATCH` 关键词必须转义**。用户输入 `it's`、`量子"计算`、`-负号`、`AND` 会让 FTS5 抛 `OperationalError`（500 错误）。必须包成引号短语：`'"' + q.replace('"','""') + '"'`
3. **`MATCH` 要求关键词 ≥3 字符**。2 字（如"天气"）返回 0 条，必须走 LIKE 路径
4. **不要用 contentless 模式**（`content=''`）。实测该模式下 `snippet()` 返回 `None`、读不到列值、按列过滤返回 0 行
5. **不要在 FTS5 表上用 LIKE**。实测不走 trigram 索引（`SCAN ... INDEX 0:L0`）且比普通表更慢（11.5ms vs 8.3ms）
6. **不要用 `bm25()` 排序**。trigram 下所有分数为 `-0.0000`，无区分度。统一按 `created_at DESC`
7. **`snippet()` 的列索引是 3**（`doc_id`=0, `source_type`=1, `tweet_id`=2, `text`=3）

---

## File Structure

| 文件 | 动作 | 职责 |
|------|------|------|
| `weibospider/search.py` | **新建** | 搜索核心逻辑：FTS5 转义、两条查询路径、Python 端高亮。纯函数为主，便于单测 |
| `weibospider/db.py` | 修改 | 建 `search_index` 表 + 全量初始化（`_create_tables`）；写操作同步维护索引；暴露 `search()` |
| `weibospider/app.py` | 修改 | 新增 `GET /api/search` 路由 |
| `weibospider/static/index.html` | 修改 | 工具栏搜索框 + 搜索标签页 + 结果渲染 + 高亮样式 |
| `tests/test_search.py` | **新建** | 搜索逻辑单测（转义、两条路径、高亮、范围覆盖） |

**为何新建 `search.py`**：`db.py` 已 881 行、`app.py` 已 1652 行。搜索的查询构造与高亮是自成一体的逻辑，独立成文件便于测试，也避免继续膨胀既有大文件。索引的写维护仍留在 `db.py`（与其他写操作同址，共用文件锁）。

---

## Task 1: 建 search_index 表并全量初始化

**Files:**
- Modify: `weibospider/db.py:50-145`（`_create_tables` 方法末尾）
- Test: `tests/test_search.py`（新建）

- [ ] **Step 1: 写失败测试**

新建 `tests/test_search.py`：

```python
import os
import sys
import tempfile
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))

from db import TweetDB


@pytest.fixture
def db():
    """Create a test database in a temp file."""
    fd, path = tempfile.mkstemp(suffix='.db')
    os.close(fd)
    tdb = TweetDB(path)
    yield tdb
    tdb.close()
    os.unlink(path)


def _mk_tweet(tid, content, retweet_content=''):
    return {
        '_id': tid, 'mblogid': 'Mb' + tid, 'user_id': '1087770692',
        'content': content, 'created_at': '2024-01-01 12:00:00',
        'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
        'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
        'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        'screen_name': '博主A', 'retweet_content': retweet_content,
    }


class TestSearchIndexTable:
    def test_search_index_table_exists(self, db):
        """search_index FTS5 table should be created on init."""
        tables = [r[0] for r in db.conn.execute(
            "SELECT name FROM sqlite_master WHERE name='search_index'"
        ).fetchall()]
        assert 'search_index' in tables

    def test_search_index_uses_trigram(self, db):
        """Table must use trigram tokenizer (MATCH on 3+ chars works)."""
        db.conn.execute(
            "INSERT INTO search_index(doc_id, source_type, tweet_id, text) "
            "VALUES ('d1','tweet','t1','今天量子计算有重大突破')"
        )
        db.conn.commit()
        n = db.conn.execute(
            "SELECT COUNT(*) FROM search_index WHERE search_index MATCH ?",
            ('量子计',)
        ).fetchone()[0]
        assert n == 1

    def test_snippet_works(self, db):
        """snippet() must return highlighted text, not None (contentless mode check)."""
        db.conn.execute(
            "INSERT INTO search_index(doc_id, source_type, tweet_id, text) "
            "VALUES ('d1','tweet','t1','今天量子计算有重大突破')"
        )
        db.conn.commit()
        hl = db.conn.execute(
            "SELECT snippet(search_index, 3, '<mark>', '</mark>', '…', 12) "
            "FROM search_index WHERE search_index MATCH ?",
            ('量子计',)
        ).fetchone()[0]
        assert hl is not None
        assert '<mark>' in hl
```

- [ ] **Step 2: 运行测试确认失败**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py -v`

Expected: 3 个测试全部 FAIL。`test_search_index_table_exists` 断言失败（`'search_index' in []`），另两个报 `OperationalError: no such table: search_index`

- [ ] **Step 3: 建表 + 全量初始化**

在 `weibospider/db.py` 的 `_create_tables` 方法内，**`self.conn.commit()`（当前第 145 行）之前**插入：

```python
            # ---- Full-text search index (FTS5 + trigram) ----
            # NOTE: all columns indexed (no UNINDEXED) so `WHERE source_type=?`
            # works; do NOT use content='' (contentless breaks snippet()).
            self.conn.executescript("""
        CREATE VIRTUAL TABLE IF NOT EXISTS search_index USING fts5(
            doc_id,
            source_type,
            tweet_id,
            text,
            tokenize='trigram'
        );
        """)
            self.conn.commit()
            self._init_search_index()
```

然后在 `close` 方法（当前第 147 行）**之前**新增方法：

```python
    def _init_search_index(self):
        """Populate search_index from existing rows if it is empty.

        Runs once on first upgrade; later writes keep it in sync incrementally.
        """
        n = self.conn.execute("SELECT COUNT(*) FROM search_index").fetchone()[0]
        if n > 0:
            return
        self.conn.executescript("""
        INSERT INTO search_index(doc_id, source_type, tweet_id, text)
            SELECT id, 'tweet', id,
                   COALESCE(content,'') || ' ' || COALESCE(retweet_content,'')
              FROM tweets;
        INSERT INTO search_index(doc_id, source_type, tweet_id, text)
            SELECT id, 'comment', tweet_id, COALESCE(content,'')
              FROM comments;
        INSERT INTO search_index(doc_id, source_type, tweet_id, text)
            SELECT id, 'annotation', tweet_id,
                   COALESCE(comment,'') || ' ' || COALESCE(selected_text,'')
              FROM annotations;
        """)
        self.conn.commit()
```

- [ ] **Step 4: 运行测试确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py -v`

Expected: 3 passed

- [ ] **Step 5: 确认没有破坏既有测试**

Run: `source venv/bin/activate && python -m pytest tests/test_db.py -q`

Expected: 全部 passed（`test_db.py` 现有测试不应受影响）

- [ ] **Step 6: 提交**

```bash
git add weibospider/db.py tests/test_search.py
git commit -m "feat: add FTS5 trigram search_index table with backfill"
```

---

## Task 2: 全量初始化要能索引已有数据

**Files:**
- Modify: `weibospider/db.py`（`_init_search_index`，Task 1 已创建）
- Test: `tests/test_search.py`

这一步验证升级场景：老库已有数据，加索引后要能立刻搜到。

- [ ] **Step 1: 写失败测试**

追加到 `tests/test_search.py`：

```python
class TestBackfill:
    def test_backfill_indexes_existing_rows(self):
        """Existing tweets/comments/annotations get indexed on first upgrade."""
        fd, path = tempfile.mkstemp(suffix='.db')
        os.close(fd)
        try:
            # 1st open: write data, then drop the index to simulate an old DB
            d1 = TweetDB(path)
            d1.insert_tweet(_mk_tweet('t1', '今天量子计算有重大突破'))
            d1.insert_comment({
                '_id': 'c1', 'tweet_id': 't1', 'content': '评论提到量子计算',
                'created_at': '2024-01-01', 'like_counts': 0, 'ip_location': '',
                'comment_user': {}, 'reply_comment': None, 'crawl_time': 0,
            })
            d1.insert_annotation({
                'id': 'a1', 'tweet_id': 't1', 'start_offset': 0, 'end_offset': 2,
                'selected_text': '今天', 'comment': '我的笔记量子很重要',
                'field': 'content', 'ranges': None,
            })
            d1.conn.execute("DROP TABLE search_index")
            d1.conn.commit()
            d1.close()

            # 2nd open: _create_tables should rebuild + backfill
            d2 = TweetDB(path)
            rows = d2.conn.execute(
                "SELECT source_type, doc_id FROM search_index ORDER BY source_type"
            ).fetchall()
            got = {(r[0], r[1]) for r in rows}
            assert ('tweet', 't1') in got
            assert ('comment', 'c1') in got
            assert ('annotation', 'a1') in got
            d2.close()
        finally:
            os.unlink(path)
```

- [ ] **Step 2: 运行测试**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestBackfill -v`

Expected: PASS（Task 1 的 `_init_search_index` 已实现该行为）。若 FAIL，检查 `_init_search_index` 是否在建表后被调用。

- [ ] **Step 3: 提交**

```bash
git add tests/test_search.py
git commit -m "test: cover search_index backfill on upgrade"
```

---

## Task 3: FTS5 关键词转义（防 500 崩溃）

**Files:**
- Create: `weibospider/search.py`
- Test: `tests/test_search.py`

- [ ] **Step 1: 写失败测试**

追加到 `tests/test_search.py`（文件顶部 import 区加 `from search import fts5_quote`）：

```python
class TestFts5Quote:
    def test_wraps_in_double_quotes(self):
        assert fts5_quote('量子计算') == '"量子计算"'

    def test_escapes_inner_double_quote(self):
        assert fts5_quote('量子"计算') == '"量子""计算"'

    @pytest.mark.parametrize('bad', [
        '量子"计算', 'AND OR NOT', 'a*', '(paren)', 'col:val',
        "it's", '量子 计算', '-负号', '', '"',
    ])
    def test_quoted_input_never_raises_in_match(self, db, bad):
        """Any user input, once quoted, must be a legal FTS5 query."""
        db.conn.execute(
            "INSERT INTO search_index(doc_id, source_type, tweet_id, text) "
            "VALUES ('d1','tweet','t1','今天量子计算有重大突破')"
        )
        db.conn.commit()
        # must not raise OperationalError
        db.conn.execute(
            "SELECT COUNT(*) FROM search_index WHERE search_index MATCH ?",
            (fts5_quote(bad),)
        ).fetchone()

    def test_raw_input_does_raise(self, db):
        """Sanity check: without quoting, FTS5 rejects these (why we escape)."""
        import sqlite3
        with pytest.raises(sqlite3.OperationalError):
            db.conn.execute(
                "SELECT COUNT(*) FROM search_index WHERE search_index MATCH ?",
                ("it's",)
            ).fetchone()
```

- [ ] **Step 2: 运行测试确认失败**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestFts5Quote -v`

Expected: 收集阶段就报 `ImportError: cannot import name 'fts5_quote' from 'search'`（文件还不存在）

- [ ] **Step 3: 建 search.py 并实现**

新建 `weibospider/search.py`：

```python
"""Global search over tweets, comments and annotations.

Two query paths (see docs/superpowers/specs/2026-08-29-global-search-design.md):
  - keyword length >= 3  -> FTS5 MATCH on search_index (uses trigram index)
  - keyword length  < 3  -> LIKE over source tables (faster than LIKE on FTS5)
"""

# FTS5 MATCH needs >= 3 chars with the trigram tokenizer; shorter keywords
# return zero rows, so they take the LIKE path instead.
MIN_MATCH_LEN = 3


def fts5_quote(s):
    """Quote a user keyword as an FTS5 phrase literal.

    Raw user input is NOT a safe FTS5 query: characters like ' " * : -
    and bare AND/OR/NOT raise OperationalError. Wrapping in double quotes
    turns the whole thing into a phrase, and inner quotes are doubled.
    """
    return '"' + (s or '').replace('"', '""') + '"'
```

- [ ] **Step 4: 运行测试确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestFts5Quote -v`

Expected: 13 passed（10 个参数化 + 3 个其他）

- [ ] **Step 5: 提交**

```bash
git add weibospider/search.py tests/test_search.py
git commit -m "feat: add fts5_quote to make any user keyword a legal FTS5 query"
```

---

## Task 4: 查询 SQL 构造（两条路径）

**Files:**
- Modify: `weibospider/search.py`
- Test: `tests/test_search.py`

- [ ] **Step 1: 写失败测试**

`tests/test_search.py` 顶部 import 改为 `from search import fts5_quote, build_search_sql, MIN_MATCH_LEN`，追加：

```python
class TestBuildSearchSql:
    def test_long_keyword_uses_match(self):
        sql, params = build_search_sql('量子计算', page=1, per_page=20)
        assert 'search_index MATCH ?' in sql
        assert 'snippet(search_index, 3' in sql
        assert params[0] == '"量子计算"'

    def test_short_keyword_uses_like_on_source_tables(self):
        sql, params = build_search_sql('量子', page=1, per_page=20)
        assert 'MATCH' not in sql
        assert 'UNION ALL' in sql
        assert '%量子%' in params

    def test_match_left_operand_is_table_not_column(self):
        """`text MATCH ?` is invalid in FTS5; must be `search_index MATCH ?`."""
        sql, _ = build_search_sql('量子计算', page=1, per_page=20)
        assert 'text MATCH' not in sql

    def test_never_orders_by_bm25(self):
        """bm25() is all -0.0000 under trigram; must sort by time."""
        for kw in ('量子计算', '量子'):
            sql, _ = build_search_sql(kw, page=1, per_page=20)
            assert 'bm25' not in sql
            assert 'created_at DESC' in sql

    def test_excludes_deleted_tweets(self):
        for kw in ('量子计算', '量子'):
            sql, _ = build_search_sql(kw, page=1, per_page=20)
            assert 'deleted' in sql

    def test_source_type_filter(self):
        sql, params = build_search_sql('量子计算', page=1, per_page=20,
                                       source_type='annotation')
        assert 'source_type' in sql
        assert 'annotation' in params

    def test_pagination_params_are_last(self):
        sql, params = build_search_sql('量子计算', page=3, per_page=10)
        assert 'LIMIT ? OFFSET ?' in sql
        assert params[-2:] == [10, 20]

    def test_date_range_filter(self):
        sql, params = build_search_sql('量子计算', page=1, per_page=20,
                                       start_date='2024-01-01',
                                       end_date='2024-12-31')
        assert '2024-01-01' in params
        assert '2024-12-31 23:59:59' in params  # end_date gets ' 23:59:59' appended (else <= '2024-12-31' excludes rest of that day)
```

- [ ] **Step 2: 运行测试确认失败**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestBuildSearchSql -v`

Expected: `ImportError: cannot import name 'build_search_sql'`

- [ ] **Step 3: 实现 build_search_sql**

追加到 `weibospider/search.py`：

```python
def build_search_sql(q, page=1, per_page=20, source_type='all',
                     start_date=None, end_date=None):
    """Build (sql, params) for a search query.

    Returns rows shaped: doc_id, source_type, tweet_id, highlight,
                         id, content, created_at, user_id, screen_name, platform
    `highlight` is filled by snippet() on the MATCH path and is NULL on the
    LIKE path (the caller highlights in Python via make_highlight()).
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
        # snippet() column index 3 == the `text` column.
        sql = f"""
        SELECT s.doc_id, s.source_type, s.tweet_id,
               snippet(search_index, 3, '<mark>', '</mark>', '…', 12) AS highlight,
               t.id, t.content, t.created_at, t.user_id, t.screen_name, t.platform
          FROM search_index s
          JOIN tweets t ON t.id = s.tweet_id
         WHERE search_index MATCH ?
           AND t.deleted = 0
           {extra}
         ORDER BY t.created_at DESC
         LIMIT ? OFFSET ?
        """
        params = [fts5_quote(q)] + filter_params + [per_page, offset]
        return sql, params

    # Path B: short keyword -> LIKE over source tables (never on the FTS5 table).
    like = f'%{q}%'
    sql = f"""
        SELECT s.doc_id, s.source_type, s.tweet_id, s.matched_text,
               NULL AS highlight,
               t.id, t.content, t.created_at, t.user_id, t.screen_name, t.platform
          FROM (
                SELECT id AS doc_id, 'tweet' AS source_type, id AS tweet_id,
                       COALESCE(content,'') || ' ' || COALESCE(retweet_content,'') AS matched_text
                  FROM tweets
                 WHERE content LIKE ? OR retweet_content LIKE ?
                UNION ALL
                SELECT id, 'comment', tweet_id, COALESCE(content,'')
                  FROM comments
                 WHERE content LIKE ?
                UNION ALL
                SELECT id, 'annotation', tweet_id,
                       COALESCE(comment,'') || ' ' || COALESCE(selected_text,'')
                  FROM annotations
                 WHERE comment LIKE ? OR selected_text LIKE ?
               ) s
          JOIN tweets t ON t.id = s.tweet_id
         WHERE t.deleted = 0
           {extra}
         ORDER BY t.created_at DESC
         LIMIT ? OFFSET ?
    """
    params = [like] * 5 + filter_params + [per_page, offset]
    return sql, params
```

- [ ] **Step 4: 运行测试确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestBuildSearchSql -v`

Expected: 8 passed

- [ ] **Step 5: 提交**

```bash
git add weibospider/search.py tests/test_search.py
git commit -m "feat: build_search_sql with MATCH and LIKE query paths"
```

---

## Task 5: Python 端高亮（LIKE 路径用）

**Files:**
- Modify: `weibospider/search.py`
- Test: `tests/test_search.py`

LIKE 路径没有 `snippet()`，高亮在 Python 端做。必须先 HTML 转义再插标签，否则微博正文里的 `<script>` 会被浏览器执行。

- [ ] **Step 1: 写失败测试**

import 改为 `from search import fts5_quote, build_search_sql, make_highlight, MIN_MATCH_LEN`，追加：

```python
class TestMakeHighlight:
    def test_wraps_keyword_in_mark(self):
        out = make_highlight('今天量子计算有突破', '量子')
        assert '<mark>量子</mark>' in out

    def test_escapes_html_to_prevent_xss(self):
        out = make_highlight('<script>x</script>量子内容', '量子')
        assert '<script>' not in out
        assert '&lt;script&gt;' in out
        assert '<mark>量子</mark>' in out

    def test_truncates_long_text_around_match(self):
        text = 'A' * 200 + '量子' + 'B' * 200
        out = make_highlight(text, '量子', context=20)
        assert len(out) < 200
        assert '<mark>量子</mark>' in out
        assert out.startswith('…')
        assert out.endswith('…')

    def test_no_match_returns_truncated_head(self):
        out = make_highlight('完全无关的内容', '量子')
        assert '<mark>' not in out
        assert '完全无关的内容' in out

    def test_case_insensitive_for_ascii(self):
        out = make_highlight('Hello World', 'hello')
        assert '<mark>Hello</mark>' in out

    def test_handles_empty_inputs(self):
        assert make_highlight('', '量子') == ''
        assert '<mark>' not in make_highlight('内容', '')
```

- [ ] **Step 2: 运行测试确认失败**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestMakeHighlight -v`

Expected: `ImportError: cannot import name 'make_highlight'`

- [ ] **Step 3: 实现 make_highlight**

`weibospider/search.py` 顶部 import 区加 `import html`，然后追加：

```python
def make_highlight(text, q, context=20):
    """Return an HTML-safe snippet of `text` with `q` wrapped in <mark>.

    Used by the LIKE path, which has no snippet(). Escapes first so that
    tweet content containing markup cannot inject HTML.
    """
    text = text or ''
    if not text:
        return ''
    if not q:
        return html.escape(text[:context * 4])

    idx = text.lower().find(q.lower())
    if idx < 0:
        return html.escape(text[:context * 4])

    start = max(0, idx - context)
    end = min(len(text), idx + len(q) + context)
    before = html.escape(text[start:idx])
    hit = html.escape(text[idx:idx + len(q)])
    after = html.escape(text[idx + len(q):end])
    out = f'{before}<mark>{hit}</mark>{after}'
    if start > 0:
        out = '…' + out
    if end < len(text):
        out = out + '…'
    return out
```

- [ ] **Step 4: 运行测试确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestMakeHighlight -v`

Expected: 6 passed

- [ ] **Step 5: 提交**

```bash
git add weibospider/search.py tests/test_search.py
git commit -m "feat: add make_highlight with HTML escaping for LIKE path"
```

---

## Task 6: DB.search() 端到端（含范围覆盖验证）

**Files:**
- Modify: `weibospider/db.py`（在 `stats` 方法前新增，当前第 675 行附近）
- Test: `tests/test_search.py`

- [ ] **Step 1: 写失败测试**

追加到 `tests/test_search.py`：

```python
@pytest.fixture
def seeded(db):
    """DB with one tweet + retweet, one comment, one annotation, one deleted tweet."""
    db.insert_tweet(_mk_tweet('t1', '今天量子计算有重大突破', '转发原文提到光刻机'))
    db.insert_tweet(_mk_tweet('t2', '天气不错适合出门'))
    db.insert_tweet(_mk_tweet('t3', '这条已删除但含量子计算'))
    db.insert_comment({
        '_id': 'c1', 'tweet_id': 't2', 'content': '评论里提到量子计算的事',
        'created_at': '2024-01-01', 'like_counts': 0, 'ip_location': '',
        'comment_user': {}, 'reply_comment': None, 'crawl_time': 0,
    })
    db.insert_annotation({
        'id': 'a1', 'tweet_id': 't2', 'start_offset': 0, 'end_offset': 2,
        'selected_text': '天气不错', 'comment': '我的笔记说量子计算很重要',
        'field': 'content', 'ranges': None,
    })
    db.batch_delete(['t3'])
    return db


class TestDbSearch:
    def test_finds_tweet_content(self, seeded):
        got = seeded.search('量子计算')
        assert any(r['source_type'] == 'tweet' and r['tweet_id'] == 't1'
                   for r in got['results'])

    def test_finds_retweet_content(self, seeded):
        got = seeded.search('光刻机')
        assert any(r['tweet_id'] == 't1' for r in got['results'])

    def test_finds_comment_content(self, seeded):
        got = seeded.search('量子计算')
        assert any(r['source_type'] == 'comment' and r['doc_id'] == 'c1'
                   for r in got['results'])

    def test_finds_annotation_comment(self, seeded):
        got = seeded.search('笔记说量子')
        assert any(r['source_type'] == 'annotation' and r['doc_id'] == 'a1'
                   for r in got['results'])

    def test_finds_annotation_selected_text(self, seeded):
        got = seeded.search('天气不错')
        assert any(r['source_type'] == 'annotation' for r in got['results'])

    def test_excludes_deleted_tweets(self, seeded):
        got = seeded.search('量子计算')
        assert all(r['tweet_id'] != 't3' for r in got['results'])

    def test_short_keyword_works(self, seeded):
        """2-char keyword must still find results (LIKE path)."""
        got = seeded.search('量子')
        assert got['total'] > 0
        assert any(r['tweet_id'] == 't1' for r in got['results'])

    def test_short_keyword_has_highlight(self, seeded):
        got = seeded.search('量子')
        assert any('<mark>' in (r.get('highlight') or '') for r in got['results'])

    def test_long_keyword_has_highlight(self, seeded):
        got = seeded.search('量子计算')
        assert any('<mark>' in (r.get('highlight') or '') for r in got['results'])

    def test_source_type_filter(self, seeded):
        got = seeded.search('量子计算', source_type='comment')
        assert got['results']
        assert all(r['source_type'] == 'comment' for r in got['results'])

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
        assert got['total'] >= 2  # tweet t1 + comment c1

    def test_new_write_is_searchable_immediately(self, seeded):
        """Sync index maintenance: a fresh insert is searchable at once."""
        seeded.insert_tweet(_mk_tweet('t9', '全新内容超导材料研究'))
        got = seeded.search('超导材料')
        assert any(r['tweet_id'] == 't9' for r in got['results'])

    def test_annotation_update_is_searchable(self, seeded):
        seeded.update_annotation('a1', '改后的笔记提到石墨烯')
        got = seeded.search('石墨烯')
        assert any(r['doc_id'] == 'a1' for r in got['results'])

    def test_annotation_delete_removes_from_index(self, seeded):
        seeded.delete_annotation('a1')
        got = seeded.search('笔记说量子')
        assert all(r['doc_id'] != 'a1' for r in got['results'])
```

- [ ] **Step 2: 运行测试确认失败**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestDbSearch -v`

Expected: 全部 FAIL，报 `AttributeError: 'TweetDB' object has no attribute 'search'`。（其中 4 个"同步维护"测试会在 Task 7 才真正变绿——此处先让 `search` 存在。）

- [ ] **Step 3: 实现 DB.search()**

`weibospider/db.py` 顶部 import 区加：

```python
from search import build_search_sql, make_highlight
```

在 `stats` 方法（当前第 675 行）**之前**新增：

```python
    def search(self, q, page=1, per_page=20, source_type='all',
               start_date=None, end_date=None):
        """Search tweets, comments and annotations for `q`.

        Returns {'results': [...], 'total': int, 'page': int, 'per_page': int}.
        """
        q = (q or '').strip()
        # Strip control chars: a bare \x00 inside fts5_quote() yields an
        # FTS5 "unterminated string" OperationalError (500). Pasting text
        # with control chars is common, so sanitize at the contract layer.
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
            # total: same query without LIMIT/OFFSET, wrapped in COUNT(*)
            count_sql = "SELECT COUNT(*) FROM (" + \
                sql.replace('LIMIT ? OFFSET ?', '') + ")"
            total = self.conn.execute(count_sql, params[:-2]).fetchone()[0]

        results = []
        for r in rows:
            d = dict(r)
            if not d.get('highlight'):
                # LIKE path: highlight the actual matched source text
                # (comment/annotation content), not just the tweet body.
                d['highlight'] = make_highlight(d.get('matched_text') or '', q)
            else:
                # IMPORTANT: snippet() does NOT HTML-escape tweet content.
                # Escape it now and restore the <mark> markers, else raw
                # `<script>` in a tweet becomes stored XSS in innerHTML.
                # Known cosmetic limit: a literal `<mark>` in original content
                # is indistinguishable from snippet's own markers and gets
                # restored as a real tag (harmless — cannot execute JS).
                d['highlight'] = escape_snippet(d['highlight'])
            results.append(d)
        return {'results': results, 'total': total,
                'page': page, 'per_page': per_page}
```

在 `weibospider/db.py` 顶部 import 区把 `from search import build_search_sql, make_highlight` 改为：

```python
from search import build_search_sql, make_highlight, escape_snippet
```

`escape_snippet` 函数需要加到 `weibospider/search.py`（在 Task 5 末尾追加，并加测试）：

```python
def escape_snippet(hl):
    """Escape snippet() output while preserving its <mark> markers.

    SQLite's snippet() does NOT HTML-escape the surrounding text, so a tweet
    containing `<script>` would pass it through raw. Escape everything, then
    restore the markers snippet() inserted.
    """
    if not hl:
        return hl
    return html.escape(hl).replace('&lt;mark&gt;', '<mark>').replace('&lt;/mark&gt;', '</mark>')
```

- [ ] **Step 4: 运行测试**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestDbSearch -v`

Expected: 大部分 PASS。以下 4 个仍 FAIL，因为索引同步维护还没做（Task 7 修复）：
`test_new_write_is_searchable_immediately`、`test_annotation_update_is_searchable`、`test_annotation_delete_removes_from_index`，以及可能的 `test_finds_comment_content` / `test_finds_annotation_*`（取决于 insert 是否已进索引）

若出现 `no such column` 或 `ambiguous column name`，检查 `build_search_sql` 里 `t.` / `s.` 前缀是否完整。

- [ ] **Step 5: 提交**

```bash
git add weibospider/db.py tests/test_search.py
git commit -m "feat: add DB.search() over tweets, comments and annotations"
```

---

## Task 7: 写操作同步维护索引

**Files:**
- Modify: `weibospider/db.py:165`（`insert_tweet`）、`:216`（`insert_comment`）、`:242`（`batch_insert_tweets`）、`:315`（`batch_insert_comments`）、`:742`（`insert_annotation`）、`:783`（`update_annotation`）、`:803`（`delete_annotation`）、`:485`（`batch_delete`）
- Test: `tests/test_search.py`（Task 6 已写好断言）

- [ ] **Step 1: 确认测试当前失败**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestDbSearch -v -k "immediately or update_is or delete_removes"`

Expected: 3 个 FAIL（新写入的内容搜不到）

- [ ] **Step 2: 新增索引维护私有方法**

在 `weibospider/db.py` 的 `_init_search_index` 方法**之后**新增。注意：这些方法**不自己加文件锁**，由调用方（已持锁的写方法）在锁内调用。

```python
    def _index_put(self, doc_id, source_type, tweet_id, text):
        """Upsert one row into search_index (delete-then-insert; FTS5 has no upsert)."""
        self.conn.execute(
            "DELETE FROM search_index WHERE doc_id=? AND source_type=?",
            (doc_id, source_type),
        )
        self.conn.execute(
            "INSERT INTO search_index(doc_id, source_type, tweet_id, text) "
            "VALUES (?,?,?,?)",
            (doc_id, source_type, tweet_id, text or ''),
        )

    def _index_delete(self, doc_id, source_type):
        self.conn.execute(
            "DELETE FROM search_index WHERE doc_id=? AND source_type=?",
            (doc_id, source_type),
        )

    def _index_tweet(self, item):
        tid = str(item.get('_id') or item.get('id') or '')
        text = (item.get('content') or '') + ' ' + (item.get('retweet_content') or '')
        self._index_put(tid, 'tweet', tid, text)

    def _index_comment(self, item):
        cid = str(item.get('_id') or item.get('id') or '')
        self._index_put(cid, 'comment', str(item.get('tweet_id') or ''),
                        item.get('content') or '')

    def _index_annotation_row(self, ann_id):
        """Re-index an annotation by reading its current row."""
        row = self.conn.execute(
            "SELECT id, tweet_id, comment, selected_text FROM annotations WHERE id=?",
            (ann_id,),
        ).fetchone()
        if row is None:
            self._index_delete(ann_id, 'annotation')
            return
        text = (row['comment'] or '') + ' ' + (row['selected_text'] or '')
        self._index_put(row['id'], 'annotation', row['tweet_id'], text)
```

- [ ] **Step 3: 在各写方法内挂钩**

每处都在**已有 `self.conn.commit()` 之前**插入索引调用（保证同一事务，索引与业务数据不会不一致）。

`insert_tweet`（约第 165-215 行），在 `self.conn.commit()` 前加：

```python
                self._index_tweet(item)
```

`insert_comment`（约第 216-241 行），在 `self.conn.commit()` 前加：

```python
                self._index_comment(item)
```

`batch_insert_tweets`（约第 242-314 行），在 `self.conn.commit()` 前加：

```python
                for it in items:
                    self._index_tweet(it)
```

`batch_insert_comments`（约第 315-354 行），在 `self.conn.commit()` 前加：

```python
                for it in items:
                    self._index_comment(it)
```

`insert_annotation`（约第 742-767 行），在 `self.conn.commit()` 前加：

```python
                self._index_annotation_row(item['id'])
```

`update_annotation`（约第 783-802 行），在 `self.conn.commit()` 前加：

```python
                self._index_annotation_row(ann_id)
```

`delete_annotation`（约第 803-815 行），在 `self.conn.commit()` 前加：

```python
                self._index_delete(ann_id, 'annotation')
```

**注意 `batch_delete`（第 485 行）不需要改**：软删除只改 `tweets.deleted`，而查询已用 `t.deleted = 0` 过滤，索引无需变动。恢复（`restore_tweets`）同理。

- [ ] **Step 4: 运行测试确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py -v`

Expected: 全部 passed（含 Task 6 剩余的 4 个）

- [ ] **Step 5: 确认没有破坏既有写入测试**

Run: `source venv/bin/activate && python -m pytest tests/test_db.py tests/test_xueqiu_comments.py -q`

Expected: 全部 passed

- [ ] **Step 6: 提交**

```bash
git add weibospider/db.py
git commit -m "feat: keep search_index in sync on tweet/comment/annotation writes"
```

---

## Task 8: GET /api/search 路由

**Files:**
- Modify: `weibospider/app.py:899`（在 `api_notes` 之后插入新路由）
- Test: `tests/test_search.py`

注意：`tests/test_app.py` 因 venv 里 werkzeug 版本过新（缺 `__version__`）无法用 Flask test client（见 AGENTS.md）。因此这里**不依赖 test client**，直接测路由函数的参数解析与 `DB.search` 的契约。

- [ ] **Step 1: 写失败测试**

追加到 `tests/test_search.py`：

```python
class TestSearchApiContract:
    """The route is a thin wrapper; verify it exists and clamps params."""

    def test_route_registered(self):
        import app as app_module
        rules = {r.rule for r in app_module.app.url_map.iter_rules()}
        assert '/api/search' in rules

    def test_per_page_is_clamped(self, seeded):
        got = seeded.search('量子计算', per_page=9999)
        assert got['per_page'] <= 100

    def test_page_floor_is_one(self, seeded):
        got = seeded.search('量子计算', page=0)
        assert got['page'] == 1

    def test_response_shape(self, seeded):
        got = seeded.search('量子计算')
        assert set(['results', 'total', 'page', 'per_page']).issubset(got.keys())
        if got['results']:
            r = got['results'][0]
            for key in ('doc_id', 'source_type', 'tweet_id', 'highlight',
                        'content', 'created_at', 'screen_name'):
                assert key in r
```

- [ ] **Step 2: 运行测试确认失败**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestSearchApiContract -v`

Expected: `test_route_registered` FAIL（`/api/search` 不在 url_map），其余可能已 PASS

- [ ] **Step 3: 新增路由**

在 `weibospider/app.py` 的 `api_notes` 函数（当前第 899-904 行）**之后**插入：

```python
@app.route('/api/search')
def api_search():
    """全局搜索：微博正文/转发原文/评论/划线笔记。

    关键词 >=3 字走 FTS5 MATCH（trigram 索引），<3 字走业务表 LIKE。
    """
    import time as _time
    q = (request.args.get('q') or '').strip()
    if not q:
        return jsonify({'results': [], 'total': 0, 'page': 1,
                        'per_page': 20, 'query': '', 'elapsed_ms': 0.0})

    page = request.args.get('page', 1, type=int)
    per_page = request.args.get('per_page', 20, type=int)
    source_type = request.args.get('source_type', 'all')
    start_date = request.args.get('start_date') or None
    end_date = request.args.get('end_date') or None

    t0 = _time.time()
    try:
        out = DB.search(q, page=page, per_page=per_page,
                        source_type=source_type,
                        start_date=start_date, end_date=end_date)
    except Exception as e:
        app.logger.exception('search failed')
        return jsonify({'error': str(e)}), 500

    out['query'] = q
    out['elapsed_ms'] = round((_time.time() - t0) * 1000, 1)
    return jsonify(out)
```

- [ ] **Step 4: 运行测试确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestSearchApiContract -v`

Expected: 4 passed

- [ ] **Step 5: 手工验证接口真的能跑**

```bash
cd weibospider && python run.py --port 5099 &
sleep 3
curl -s 'http://127.0.0.1:5099/api/search?q=量子计算' | head -c 400
echo
curl -s 'http://127.0.0.1:5099/api/search?q=量子' | head -c 400
echo
curl -s "http://127.0.0.1:5099/api/search?q=it's" | head -c 200
echo
kill %1
```

Expected: 三次都返回 JSON（含 `results` / `total` / `elapsed_ms`），**均不返回 500**。本地库为空时 `total` 可能是 0，这是正常的——重点是不报错。

- [ ] **Step 6: 提交**

```bash
git add weibospider/app.py tests/test_search.py
git commit -m "feat: add GET /api/search endpoint"
```

---

## Task 9: 前端工具栏搜索框

**Files:**
- Modify: `weibospider/static/index.html`
- Test: `tests/test_frontend.py`

`tests/test_frontend.py` 的既有做法是对 `index.html` 做字符串断言，沿用该风格。

- [ ] **Step 1: 写失败测试**

追加到 `tests/test_frontend.py`（文件末尾；若已有读取 html 的辅助函数则复用）：

```python
import os

HTML_PATH = os.path.join(os.path.dirname(__file__), '..',
                         'weibospider', 'static', 'index.html')


def _html():
    with open(HTML_PATH, encoding='utf-8') as f:
        return f.read()


class TestSearchUI:
    def test_search_input_exists(self):
        assert 'id="search-input"' in _html()

    def test_search_tab_exists(self):
        h = _html()
        assert 'data-tab="search"' in h
        assert 'id="search-view"' in h

    def test_calls_search_api(self):
        assert '/api/search?' in _html()

    def test_mark_style_defined(self):
        assert 'mark {' in _html() or '.search-hl mark' in _html()

    def test_enter_key_triggers_search(self):
        h = _html()
        assert 'doSearch' in h
```

- [ ] **Step 2: 运行测试确认失败**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py::TestSearchUI -v`

Expected: 5 个 FAIL

- [ ] **Step 3: 加搜索框和样式**

在 `index.html` 的 `.toolbar-actions` div（约第 23 行定义的 class，在 HTML body 的工具栏区域）内，**"导出 PDF"按钮之前**插入：

```html
      <input id="search-input" class="search-input" type="search"
             placeholder="搜索微博 / 评论 / 笔记…" autocomplete="off">
      <button class="btn btn-primary btn-sm" onclick="doSearch()">搜索</button>
```

在 `<style>` 块末尾（`</style>` 之前）追加：

```css
/* Global search */
.search-input { padding: 6px 10px; border: 1px solid #ddd; border-radius: 6px; font-size: 12px; outline: none; width: 200px; }
.search-input:focus { border-color: #4a9eff; }
mark { background: #fff3a3; color: inherit; padding: 0 1px; border-radius: 2px; }
.search-meta { font-size: 12px; color: #888; padding: 8px 0; }
.search-empty { text-align: center; color: #999; font-size: 13px; padding: 40px 0; }
.search-src { display: inline-block; font-size: 10px; padding: 1px 6px; border-radius: 10px; margin-right: 6px; vertical-align: middle; }
.search-src.tweet { background: #e8f0fe; color: #1a73e8; }
.search-src.comment { background: #fef0e8; color: #e67e22; }
.search-src.annotation { background: #e8f8ee; color: #27ae60; }
.search-hl { line-height: 1.7; font-size: 14px; word-break: break-word; }
```

在 tabs 容器内（`.tabs` div，约第 40 行定义的 class）追加一个标签按钮：

```html
      <button class="tab" data-tab="search">搜索</button>
```

在最后一个视图容器之后、`<div id="lightbox">` 之前插入搜索视图：

```html
  <div id="search-view" style="display:none">
    <div class="search-meta" id="search-meta"></div>
    <div id="search-results"></div>
  </div>
```

- [ ] **Step 4: 运行测试确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py::TestSearchUI -v`

Expected: `test_enter_key_triggers_search` 仍 FAIL（`doSearch` 未定义，Task 10 加），其余 4 passed

- [ ] **Step 5: 提交**

```bash
git add weibospider/static/index.html tests/test_frontend.py
git commit -m "feat: add search input, tab and styles to frontend"
```

---

## Task 10: 前端搜索逻辑与结果渲染

**Files:**
- Modify: `weibospider/static/index.html`（`<script>` 块）
- Test: `tests/test_frontend.py`

- [ ] **Step 1: 确认测试当前失败**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py::TestSearchUI::test_enter_key_triggers_search -v`

Expected: FAIL

- [ ] **Step 2: 加搜索 JS**

在 `index.html` 的 `<script>` 块末尾追加。注意 `highlight` 已是后端生成的安全 HTML（`snippet()` 输出经 `escape_snippet()` 转义，`make_highlight()` 本身先转义），用 `innerHTML` 是安全的；其余用户可见文本一律走 `escapeHtml`。

```javascript
// ---- Global search ----
let searchState = { q: '', page: 1, perPage: 20, sourceType: 'all', total: 0 };

function escapeHtml(s) {
  return String(s == null ? '' : s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

const SRC_LABEL = { tweet: '微博', comment: '评论', annotation: '笔记' };

function doSearch(page) {
  const input = document.getElementById('search-input');
  const q = (input && input.value || '').trim();
  if (!q) { toast('请输入搜索关键词'); return; }
  searchState.q = q;
  searchState.page = page || 1;
  switchTab('search');
  runSearch();
}

function runSearch() {
  const meta = document.getElementById('search-meta');
  const box = document.getElementById('search-results');
  meta.textContent = '搜索中…';
  box.innerHTML = '';
  const p = new URLSearchParams({
    q: searchState.q,
    page: searchState.page,
    per_page: searchState.perPage,
    source_type: searchState.sourceType,
  });
  fetch('/api/search?' + p.toString())
    .then(r => r.json())
    .then(data => {
      if (data.error) { meta.textContent = '搜索出错：' + data.error; return; }
      searchState.total = data.total || 0;
      renderSearchResults(data);
    })
    .catch(e => { meta.textContent = '搜索失败：' + e; });
}

function renderSearchResults(data) {
  const meta = document.getElementById('search-meta');
  const box = document.getElementById('search-results');
  const results = data.results || [];
  meta.textContent = '“' + data.query + '” 共 ' + data.total +
                     ' 条结果（' + data.elapsed_ms + 'ms）';
  if (!results.length) {
    box.innerHTML = '<div class="search-empty">没有找到匹配内容</div>';
    return;
  }
  box.innerHTML = results.map(r => {
    const label = SRC_LABEL[r.source_type] || r.source_type;
    return '<div class="card"><div class="card-body">' +
      '<div class="card-meta"><span class="card-meta-left">' +
        '<span class="search-src ' + escapeHtml(r.source_type) + '">' +
          escapeHtml(label) + '</span>' +
        escapeHtml(r.screen_name || '') + ' · ' +
        escapeHtml(r.created_at || '') +
      '</span></div>' +
      // highlight is server-generated and already HTML-escaped
      '<div class="search-hl">' + (r.highlight || '') + '</div>' +
      '</div></div>';
  }).join('');
  renderSearchPager();
}

function renderSearchPager() {
  const total = searchState.total, per = searchState.perPage;
  const pages = Math.ceil(total / per);
  if (pages <= 1) return;
  const box = document.getElementById('search-results');
  const cur = searchState.page;
  let html = '<div class="search-meta" style="text-align:center">';
  if (cur > 1) html += '<button class="btn btn-ghost" onclick="doSearch(' + (cur - 1) + ')">上一页</button> ';
  html += ' 第 ' + cur + ' / ' + pages + ' 页 ';
  if (cur < pages) html += '<button class="btn btn-ghost" onclick="doSearch(' + (cur + 1) + ')">下一页</button>';
  html += '</div>';
  box.insertAdjacentHTML('beforeend', html);
}

document.addEventListener('keydown', function (e) {
  if (e.key === 'Enter' && e.target && e.target.id === 'search-input') {
    e.preventDefault();
    doSearch(1);
  }
});
```

- [ ] **Step 3: 接入 switchTab**

找到既有的 `switchTab` 函数（管理各 tab 视图显隐处）。把 `search` 视图加入显隐逻辑：显示 `search` tab 时 `document.getElementById('search-view').style.display = ''`，切到其他 tab 时设为 `'none'`，与其他视图的处理方式保持一致。

`toast(...)` 是页面既有函数（配置保存时用过），直接复用。

- [ ] **Step 4: 运行测试确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py -v`

Expected: 全部 passed（含 `test_enter_key_triggers_search`）

- [ ] **Step 5: 浏览器手工验证**

```bash
cd weibospider && python run.py --dev --port 5099
```

打开 `http://127.0.0.1:5099`，然后：

1. 工具栏输入 `量子计算` 回车 → 跳到"搜索"标签，显示结果数与耗时
2. 输入 2 字词 `量子` 回车 → 仍有结果（走 LIKE 路径）
3. 输入 `it's` 回车 → 显示"没有找到匹配内容"，**不是** "搜索出错"
4. Console 无报错
5. 结果里关键词有黄色高亮

本地库为空时可先抓一点数据，或手工插几条测试数据再验。

- [ ] **Step 6: 提交**

```bash
git add weibospider/static/index.html
git commit -m "feat: wire up frontend search with paging and highlight"
```

---

## Task 11: 全量回归 + 索引重建入口

**Files:**
- Modify: `weibospider/app.py`（新增 `POST /api/search/reindex`）
- Test: `tests/test_search.py`

线上是老库，首次部署会自动 backfill。但如果索引因故损坏或漂移，需要一个不用重启容器就能重建的入口。

- [ ] **Step 1: 写失败测试**

追加到 `tests/test_search.py`：

```python
class TestReindex:
    def test_reindex_route_registered(self):
        import app as app_module
        rules = {r.rule for r in app_module.app.url_map.iter_rules()}
        assert '/api/search/reindex' in rules

    def test_rebuild_search_index_repopulates(self, seeded):
        seeded.conn.execute("DELETE FROM search_index")
        seeded.conn.commit()
        assert seeded.search('量子计算')['total'] == 0
        n = seeded.rebuild_search_index()
        assert n > 0
        assert seeded.search('量子计算')['total'] > 0
```

- [ ] **Step 2: 运行测试确认失败**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestReindex -v`

Expected: 2 FAIL（路由不存在、`rebuild_search_index` 不存在）

- [ ] **Step 3: 实现重建方法**

在 `weibospider/db.py` 的 `_init_search_index` 之后新增：

```python
    def rebuild_search_index(self):
        """Drop and rebuild the whole search index. Returns row count."""
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (rebuild_search_index)")
            try:
                self.conn.execute("DELETE FROM search_index")
                self.conn.commit()
                self._init_search_index()
                return self.conn.execute(
                    "SELECT COUNT(*) FROM search_index"
                ).fetchone()[0]
            finally:
                self._release_file_lock()
```

在 `weibospider/app.py` 的 `api_search` 之后新增：

```python
@app.route('/api/search/reindex', methods=['POST'])
def api_search_reindex():
    """重建全文索引（数据漂移或首次升级时手工触发）。"""
    try:
        n = DB.rebuild_search_index()
    except Exception as e:
        app.logger.exception('reindex failed')
        return jsonify({'error': str(e)}), 500
    DB.insert_log('search', 'reindex', detail=f'rows={n}', status='ok')
    return jsonify({'ok': True, 'indexed': n})
```

- [ ] **Step 4: 运行测试确认通过**

Run: `source venv/bin/activate && python -m pytest tests/test_search.py::TestReindex -v`

Expected: 2 passed

- [ ] **Step 5: 跑全量测试套件**

Run: `source venv/bin/activate && python -m pytest tests/ -q`

Expected: 除 `test_app.py` / `test_integration.py` 里依赖 Flask test client 的既有 werkzeug 环境问题（见 AGENTS.md，与本次改动无关）外，全部通过。**搜索相关测试必须全绿。**

对比基线：改动前先跑一次 `git stash && python -m pytest tests/ -q && git stash pop`，确认失败项与改动前一致。

- [ ] **Step 6: 提交**

```bash
git add weibospider/db.py weibospider/app.py tests/test_search.py
git commit -m "feat: add POST /api/search/reindex to rebuild the index"
```

---

## Task 12: 真实数据量性能验证

**Files:**
- 不改代码，只验证

线上有约 5k 微博 + 11w 评论。本地库是空的，需要造等量数据确认性能与设计文档的实测基准一致。

- [ ] **Step 1: 造数据并计时**

```bash
source venv/bin/activate && python - <<'PY'
import os, tempfile, sys, time, random
sys.path.insert(0, 'weibospider')
from db import TweetDB

fd, path = tempfile.mkstemp(suffix='.db'); os.close(fd)
db = TweetDB(path)
vocab = ['市场','大涨','下跌','基金','收益','风险','管理','投资','分析',
         '天气','真好','开心','今天','明天','公司','财报','业绩','增长']
random.seed(1)

t0 = time.time()
tweets = []
for i in range(5000):
    tweets.append({
        '_id': f't{i}', 'mblogid': f'M{i}', 'user_id': '1',
        'content': ''.join(random.choices(vocab, k=random.randint(6, 25))),
        'created_at': '2024-01-01 12:00:00', 'reposts_count': 0,
        'comments_count': 0, 'attitudes_count': 0, 'pic_urls': '[]',
        'pic_num': 0, 'source': '', 'ip_location': '', 'is_retweet': 0,
        'retweet_id': None, 'url': '', 'crawl_time': 0,
        'screen_name': '博主', 'retweet_content': '',
    })
tweets[0]['content'] = '这里提到量子计算突破'
db.batch_insert_tweets(tweets)
print(f'insert 5k tweets: {time.time()-t0:.1f}s')

t0 = time.time()
cs = []
for i in range(110000):
    cs.append({
        '_id': f'c{i}', 'tweet_id': f't{i % 5000}',
        'content': ''.join(random.choices(vocab, k=random.randint(5, 20))),
        'created_at': '2024-01-01', 'like_counts': 0, 'ip_location': '',
        'comment_user': {}, 'reply_comment': None, 'crawl_time': 0,
    })
cs[0]['content'] = '评论里也提到量子计算突破'
db.batch_insert_comments(cs)
print(f'insert 110k comments: {time.time()-t0:.1f}s')

for q in ('量子计算', '量子', '天气', '大涨'):
    t0 = time.time()
    out = db.search(q, per_page=20)
    print(f'search {q!r:8} ({len(q)} chars): {out["total"]:6} hits  '
          f'{(time.time()-t0)*1000:7.1f}ms')

print('db size:', round(os.path.getsize(path) / 1024 / 1024, 1), 'MB')
db.close(); os.unlink(path)
PY
```

Expected:
- ≥3 字查询在 **100ms 以内**（有 trigram 索引）
- 2 字查询在 **500ms 以内**（全表扫描 11w 行）
- 批量写入的索引开销不超过原来的 2 倍

- [ ] **Step 2: 结果不达标时的处理**

若 2 字查询超过 1s：给 `comments.content` 加普通索引无用（LIKE `%x%` 用不上），改为在设计文档"未来扩展"里记录，并把前端最小搜索长度提到 2 字以上（`doSearch` 里加 `if (q.length < 2) { toast('关键词至少 2 个字'); return; }`）。**不要**为此引入 jieba —— 那是独立的一次改动。

若写入明显变慢（超过 2 倍）：把 `batch_insert_*` 里的逐条 `_index_*` 改成一次 `executemany`。

**重要发现（2026-08-29 实测）**：即使改成 `executemany`，FTS5 的 `DELETE FROM search_index WHERE doc_id=? AND source_type=?` 仍是 O(index_size) 每行（trigram 表上按非 rowid 列删除要全扫索引）。实测：索引 5k 行时删除 20k 行要 9s，索引 10k 行要 17.4s；到生产的 114k 行，一次全量评论抓取的删除阶段要 ~16 分钟。

**正确解法**：加一张普通表 `search_doc(doc_id, source_type, fts_rowid, PRIMARY KEY(doc_id, source_type))` 作为 doc→FTS5 rowid 的映射。删除时先查这张表拿 rowid，再 `DELETE FROM search_index WHERE rowid=?`（O(1)）。实测同一场景降到 **76k rows/s，且不随索引大小退化**（5k 行索引下 5500 条混合批量 0.07s）。

实现要点：
1. `_create_tables` 里建 `search_doc` 表
2. `_backfill_search_index` 回填时同步填充 `search_doc`（`SELECT doc_id, source_type, rowid FROM search_index`）
3. `_index_put`：查 `search_doc` → 有则按 rowid 删；INSERT 后用 `last_insert_rowid()` 拿到新 rowid 写回 `search_doc`
4. `_index_delete`：查 `search_doc` 拿 rowid 再删
5. 批量路径：对每个 item 查 `search_doc` 判断是否存在，存在的一批按 rowid 删，新的一批直接 INSERT，`executemany` 后按 `last_insert_rowid()` 倒推 rowid 写回 `search_doc`

- [ ] **Step 3: 把实测数字回写设计文档**

把本次 5k+11w 真实规模的数字追加到 `docs/superpowers/specs/2026-08-29-global-search-design.md` 的 2.2 节，标注"真实规模实测"。

- [ ] **Step 4: 提交**

```bash
git add docs/superpowers/specs/2026-08-29-global-search-design.md
git commit -m "docs: record search performance at production data scale"
```

---

## Task 13: 部署前检查

**Files:**
- 不改代码，只验证

- [ ] **Step 1: 确认容器 SQLite 支持 trigram**

```bash
ssh weibo 'cd /opt/weibospider && docker compose exec -T weibospider python3 -c "
import sqlite3
print(\"sqlite:\", sqlite3.sqlite_version)
c = sqlite3.connect(\":memory:\")
c.execute(\"CREATE VIRTUAL TABLE t USING fts5(x, tokenize=trigram)\")
print(\"trigram: OK\")
"'
```

Expected: `sqlite: 3.46.1` + `trigram: OK`（已于设计阶段验证过一次，部署前复核）

- [ ] **Step 2: 估算线上首次启动的 backfill 耗时**

首次启动会对 5k 微博 + 11w 评论 + 若干笔记做全量索引。按 Task 12 的实测数字估算，应在**秒级到十几秒**。这发生在 `TweetDB.__init__` 里，会**阻塞 Flask 启动**。

若 Task 12 显示 backfill 超过 30s，改为惰性：`_init_search_index` 只建表不填充，由 `POST /api/search/reindex` 手工触发首次填充；并在前端搜索无结果时提示"索引未建立，请先重建索引"。

- [ ] **Step 3: 确认数据目录权限没变**

索引写在同一个 `data.db` 里，不新增文件，权限要求不变（容器内 UID 1000 需可写 `data/`，见 AGENTS.md）。无需额外 chown。

- [ ] **Step 4: 部署**

```bash
git push
```

push 到 master 触发 GitHub Actions 自动部署。

- [ ] **Step 5: 线上验证**

```bash
sleep 90
curl -s 'http://43.130.247.183:5050/api/search?q=量子计算' | head -c 300
echo
curl -s 'http://43.130.247.183:5050/api/search?q=股票' | head -c 300
echo
curl -s "http://43.130.247.183:5050/api/search?q=it's" -o /dev/null -w 'status=%{http_code}\n'
ssh weibo 'cd /opt/weibospider && docker compose logs --tail 30'
```

Expected:
- 前两个返回 JSON 且 `total > 0`（线上有真实数据）
- 第三个 `status=200`（不是 500）
- 容器日志无 traceback

- [ ] **Step 6: 浏览器验证线上**

打开 `http://43.130.247.183:5050`，搜一个真实关键词，确认结果有高亮、分页可用、切标签正常。

---

## Self-Review

**1. Spec coverage**

| 设计文档章节 | 对应 Task |
|---|---|
| 1.1 搜索范围（正文/转发/评论/笔记/划线原文） | Task 6（`TestDbSearch` 五个 `test_finds_*` 逐项覆盖） |
| 1.2 方案 C（工具栏 + 独立页） | Task 9（搜索框 + tab）、Task 10（结果页） |
| 1.3 同步更新索引 | Task 7 |
| 2.2 性能基准 | Task 12 |
| 2.3 不用 BM25、按时间排序 | Task 4（`test_never_orders_by_bm25`） |
| 2.4 两条查询路径 | Task 4 |
| 3.1 FTS5 表结构（不用 UNINDEXED / 不用 contentless） | Task 1（`test_snippet_works` 守住 contentless 回归） |
| 3.2 数据来源映射 | Task 1（backfill SQL）、Task 7（增量维护） |
| 3.3.2 初始化逻辑 | Task 2 |
| 3.3.3 并发控制（复用文件锁） | Task 7（索引方法在调用方锁内执行） |
| 4.1 API 参数与响应 | Task 8 |
| 4.2 SQL 与 MATCH 转义 | Task 3、Task 4 |
| Python 端高亮 + XSS | Task 5 |
| 5 前端设计 | Task 9、Task 10 |

**2. Placeholder scan**：无 TBD / "类似 Task N" / "适当处理错误"。每个改代码的步骤都给了完整代码块与确切行号锚点。

**3. Type consistency**

- `fts5_quote(s)` — 定义于 Task 3，用于 Task 4
- `build_search_sql(q, page, per_page, source_type, start_date, end_date)` — 定义于 Task 4，用于 Task 6，签名一致
- `make_highlight(text, q, context=20)` — 定义于 Task 5，用于 Task 6，签名一致
- `MIN_MATCH_LEN` — 定义于 Task 3，用于 Task 4
- `_index_put / _index_delete / _index_tweet / _index_comment / _index_annotation_row` — 定义于 Task 7 Step 2，全部在 Step 3 使用
- `_init_search_index()` — 定义于 Task 1，被 Task 11 的 `rebuild_search_index` 复用
- `DB.search(...)` 返回 `{'results','total','page','per_page'}` — Task 6 定义，Task 8 在其上加 `query` / `elapsed_ms`，与设计文档 4.1 一致
- `doSearch(page)` / `runSearch()` / `renderSearchResults(data)` / `renderSearchPager()` / `escapeHtml(s)` — 均定义并调用于 Task 10；`toast()` 与 `switchTab()` 是页面既有函数

**修复记录**：初稿在 Task 6 用了 `DB.search_tweets()`、Task 8 调 `DB.search()`，已统一为 `search()`。
