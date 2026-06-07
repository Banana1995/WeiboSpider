# 微博内容管理器 - 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 基于 WeiboSpider 改造，每天全量抓取指定博主的微博+评论到 SQLite，通过 Flask Web 界面瀑布流展示、批量软删除。

**Architecture:** Scrapy spiders 写入 SQLite pipeline → Flask REST API 提供数据 → Vanilla JS SPA 展示/管理。APScheduler 每天定时触发，也支持手动触发。单进程运行。

**Tech Stack:** Python, Scrapy 2.5.1, Flask 2.3, SQLite, APScheduler 3.10, Vanilla JS, pytest

---

### Task 1: 更新依赖

**Files:**
- Modify: `weibospider/requirements.txt`

- [ ] **Step 1: 更新 requirements.txt**

```bash
cat > weibospider/requirements.txt << 'EOF'
Scrapy==2.5.1
python_dateutil
cryptography==36.0.2
pyOpenSSL==22.0.0
Twisted==22.10.0
Flask==2.3.0
APScheduler==3.10.0
pytest==7.4.0
EOF
```

- [ ] **Step 2: 安装依赖**

```bash
cd weibospider && pip install Flask==2.3.0 APScheduler==3.10.0 pytest==7.4.0
```

- [ ] **Step 3: Commit**

```bash
git add weibospider/requirements.txt
git commit -m "build: add Flask, APScheduler, pytest dependencies"
```

---

### Task 2: 创建 db.py — SQLite 数据层

**Files:**
- Create: `weibospider/db.py`
- Create: `tests/test_db.py`

- [ ] **Step 1: 编写 db.py 的测试**

```bash
mkdir -p tests
```

```python
# tests/test_db.py
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


class TestTweetDB:
    def test_create_tables(self, db):
        """Tables should be created on init."""
        tables = db.conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table'"
        ).fetchall()
        table_names = [t[0] for t in tables]
        assert 'tweets' in table_names
        assert 'comments' in table_names

    def test_insert_tweet(self, db):
        db.insert_tweet({
            '_id': '123456', 'mblogid': 'Mb123', 'user_id': '1087770692',
            'content': 'hello world', 'created_at': '2024-01-01 12:00:00',
            'reposts_count': 0, 'comments_count': 2, 'attitudes_count': 10,
            'pic_urls': '[]', 'pic_num': 0, 'source': 'iPhone',
            'ip_location': '北京', 'is_retweet': 0, 'retweet_id': None,
            'url': 'https://weibo.com/1087770692/Mb123', 'crawl_time': 1700000000,
        })
        row = db.conn.execute("SELECT * FROM tweets WHERE id='123456'").fetchone()
        assert row is not None
        assert row[2] == 'hello world'
        assert row[14] == 0  # deleted=0

    def test_insert_tweet_ignore_duplicate(self, db):
        tweet = {
            '_id': '123456', 'mblogid': 'Mb123', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 12:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        }
        db.insert_tweet(tweet)
        db.insert_tweet(tweet)
        count = db.conn.execute("SELECT COUNT(*) FROM tweets WHERE id='123456'").fetchone()[0]
        assert count == 1

    def test_insert_comment(self, db):
        db.insert_tweet({
            '_id': '123456', 'mblogid': 'Mb123', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 12:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_comment({
            '_id': 'c1', 'tweet_id': '123456', 'content': 'nice',
            'created_at': '2024-01-01 13:00:00', 'like_counts': 5,
            'ip_location': '上海', 'comment_user': '{"nick_name":"A"}',
            'reply_comment': None, 'crawl_time': 1700000000,
        })
        row = db.conn.execute("SELECT * FROM comments WHERE id='c1'").fetchone()
        assert row is not None
        assert row[2] == '123456'
        assert row[3] == 'nice'

    def test_get_tweets_pagination(self, db):
        for i in range(5):
            db.insert_tweet({
                '_id': str(i), 'mblogid': f'Mb{i}', 'user_id': '1087770692',
                'content': f'tweet {i}', 'created_at': f'2024-01-01 0{i}:00:00',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })
        page1 = db.get_tweets(page=1, per_page=2, sort='desc')
        assert len(page1) == 2
        assert page1[0]['id'] == '4'  # newest first
        page2 = db.get_tweets(page=2, per_page=2, sort='desc')
        assert len(page2) == 2
        assert page2[0]['id'] == '2'
        page3 = db.get_tweets(page=3, per_page=2, sort='desc')
        assert len(page3) == 1
        assert page3[0]['id'] == '0'

    def test_get_tweets_excludes_deleted(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'kept', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_tweet({
            '_id': '2', 'mblogid': 'Mb2', 'user_id': '1087770692',
            'content': 'deleted', 'created_at': '2024-01-01 11:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.batch_delete(['2'])
        results = db.get_tweets(page=1, per_page=10)
        assert len(results) == 1
        assert results[0]['id'] == '1'

    def test_get_tweets_deleted(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'deleted', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.batch_delete(['1'])
        results = db.get_tweets(page=1, per_page=10, deleted='all')
        assert len(results) == 1
        assert results[0]['deleted'] == 1

    def test_get_comments(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        for i in range(3):
            db.insert_comment({
                '_id': f'c{i}', 'tweet_id': '1', 'content': f'comment {i}',
                'created_at': f'2024-01-01 1{i}:00:00', 'like_counts': 0,
                'ip_location': '', 'comment_user': '{}',
                'reply_comment': None, 'crawl_time': 0,
            })
        comments = db.get_comments('1')
        assert len(comments) == 3

    def test_batch_delete(self, db):
        for i in range(3):
            db.insert_tweet({
                '_id': str(i), 'mblogid': f'Mb{i}', 'user_id': '1087770692',
                'content': f'tweet {i}', 'created_at': '2024-01-01 0{i}:00:00',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })
        count = db.batch_delete(['0', '1'])
        assert count == 2
        for id_ in ['0', '1']:
            row = db.conn.execute(f"SELECT deleted FROM tweets WHERE id='{id_}'").fetchone()
            assert row[0] == 1
        row = db.conn.execute("SELECT deleted FROM tweets WHERE id='2'").fetchone()
        assert row[0] == 0

    def test_restore_tweets(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.batch_delete(['1'])
        count = db.restore_tweets(['1'])
        assert count == 1
        row = db.conn.execute("SELECT deleted FROM tweets WHERE id='1'").fetchone()
        assert row[0] == 0

    def test_get_tweet(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        tweet = db.get_tweet('1')
        assert tweet is not None
        assert tweet['content'] == 'hello'

    def test_get_tweet_ids(self, db):
        for i in range(3):
            db.insert_tweet({
                '_id': str(i), 'mblogid': f'Mb{i}', 'user_id': '1087770692',
                'content': f'tweet {i}', 'created_at': '2024-01-01 0{i}:00:00',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })
        ids = db.get_tweet_ids()
        assert ids == ['Mb0', 'Mb1', 'Mb2']

    def test_stats(self, db):
        for i in range(5):
            db.insert_tweet({
                '_id': str(i), 'mblogid': f'Mb{i}', 'user_id': '1087770692',
                'content': f'tweet {i}', 'created_at': '2024-01-01 0{i}:00:00',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })
        db.batch_delete(['0', '1'])
        db.insert_comment({
            '_id': 'c1', 'tweet_id': '2', 'content': 'nice',
            'created_at': '2024-01-01 13:00:00', 'like_counts': 0,
            'ip_location': '', 'comment_user': '{}',
            'reply_comment': None, 'crawl_time': 0,
        })
        stats = db.stats()
        assert stats['total_tweets'] == 5
        assert stats['deleted_tweets'] == 2
        assert stats['total_comments'] == 1
```

- [ ] **Step 2: 运行测试验证失败**

```bash
cd weibospider && PYTHONPATH=. python -m pytest ../tests/test_db.py -v
```
Expected: FAIL — `ModuleNotFoundError: No module named 'db'`

- [ ] **Step 3: 实现 db.py**

```python
# weibospider/db.py
import json
import os
import sqlite3
import time
from datetime import datetime


class TweetDB:
    def __init__(self, db_path=None):
        if db_path is None:
            db_path = os.path.join(os.path.dirname(__file__), 'data.db')
        self.conn = sqlite3.connect(db_path, check_same_thread=False)
        self.conn.row_factory = sqlite3.Row
        self.conn.execute("PRAGMA journal_mode=WAL")
        self._create_tables()

    def _create_tables(self):
        self.conn.executescript("""
        CREATE TABLE IF NOT EXISTS tweets (
            id              TEXT PRIMARY KEY,
            mblogid         TEXT,
            user_id         TEXT NOT NULL,
            content         TEXT NOT NULL,
            created_at      TEXT,
            reposts_count   INTEGER DEFAULT 0,
            comments_count  INTEGER DEFAULT 0,
            attitudes_count INTEGER DEFAULT 0,
            pic_urls        TEXT,
            pic_num         INTEGER DEFAULT 0,
            source          TEXT,
            ip_location     TEXT,
            is_retweet      INTEGER DEFAULT 0,
            retweet_id      TEXT,
            url             TEXT,
            crawl_time      INTEGER,
            deleted         INTEGER DEFAULT 0,
            deleted_at      TEXT
        );
        CREATE TABLE IF NOT EXISTS comments (
            id              TEXT PRIMARY KEY,
            tweet_id        TEXT NOT NULL,
            content         TEXT NOT NULL,
            created_at      TEXT,
            like_counts     INTEGER DEFAULT 0,
            ip_location     TEXT,
            comment_user    TEXT,
            reply_comment   TEXT,
            crawl_time      INTEGER,
            FOREIGN KEY (tweet_id) REFERENCES tweets(id)
        );
        CREATE INDEX IF NOT EXISTS idx_tweets_user_id ON tweets(user_id);
        CREATE INDEX IF NOT EXISTS idx_tweets_created_at ON tweets(created_at);
        CREATE INDEX IF NOT EXISTS idx_tweets_deleted ON tweets(deleted);
        CREATE INDEX IF NOT EXISTS idx_comments_tweet_id ON comments(tweet_id);
        """)
        self.conn.commit()

    def close(self):
        self.conn.close()

    def insert_tweet(self, item):
        self.conn.execute("""
        INSERT OR IGNORE INTO tweets
            (id, mblogid, user_id, content, created_at, reposts_count,
             comments_count, attitudes_count, pic_urls, pic_num,
             source, ip_location, is_retweet, retweet_id, url, crawl_time, deleted)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
        """, (
            item['_id'], item.get('mblogid'), item.get('user_id', ''),
            item['content'], item.get('created_at'),
            item.get('reposts_count', 0), item.get('comments_count', 0),
            item.get('attitudes_count', 0),
            json.dumps(item.get('pic_urls', [])) if isinstance(item.get('pic_urls'), list) else (item.get('pic_urls') or '[]'),
            item.get('pic_num', 0), item.get('source', ''),
            item.get('ip_location', ''), int(item.get('is_retweet', False)),
            item.get('retweet_id'), item.get('url', ''),
            item.get('crawl_time', int(time.time())),
        ))
        self.conn.commit()

    def insert_comment(self, item):
        self.conn.execute("""
        INSERT OR IGNORE INTO comments
            (id, tweet_id, content, created_at, like_counts,
             ip_location, comment_user, reply_comment, crawl_time)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        """, (
            item['_id'], item['tweet_id'], item['content'],
            item.get('created_at'), item.get('like_counts', 0),
            item.get('ip_location', ''),
            json.dumps(item.get('comment_user', {})) if isinstance(item.get('comment_user'), dict) else (item.get('comment_user') or '{}'),
            json.dumps(item.get('reply_comment')) if isinstance(item.get('reply_comment'), dict) else (item.get('reply_comment')),
            item.get('crawl_time', int(time.time())),
        ))
        self.conn.commit()

    def get_tweets(self, page=1, per_page=20, sort='desc', deleted='exclude'):
        offset = (page - 1) * per_page
        order = 'DESC' if sort == 'desc' else 'ASC'

        where = ''
        params = []
        if deleted == 'exclude':
            where = 'WHERE deleted = 0'
        elif deleted == 'only':
            where = 'WHERE deleted = 1'

        sql = f"SELECT * FROM tweets {where} ORDER BY created_at {order} LIMIT ? OFFSET ?"
        params = [per_page, offset]
        rows = self.conn.execute(sql, params).fetchall()
        results = []
        for row in rows:
            d = dict(row)
            d['pic_urls'] = json.loads(d.get('pic_urls', '[]') or '[]')
            d['is_retweet'] = bool(d.get('is_retweet'))
            d['deleted'] = bool(d.get('deleted'))
            results.append(d)
        return results

    def get_tweet(self, tweet_id):
        row = self.conn.execute("SELECT * FROM tweets WHERE id=?", (tweet_id,)).fetchone()
        if row is None:
            return None
        d = dict(row)
        d['pic_urls'] = json.loads(d.get('pic_urls', '[]') or '[]')
        d['is_retweet'] = bool(d.get('is_retweet'))
        d['deleted'] = bool(d.get('deleted'))
        return d

    def get_comments(self, tweet_id):
        rows = self.conn.execute(
            "SELECT * FROM comments WHERE tweet_id=? ORDER BY created_at",
            (tweet_id,)
        ).fetchall()
        results = []
        for row in rows:
            d = dict(row)
            d['comment_user'] = json.loads(d.get('comment_user', '{}') or '{}')
            if d.get('reply_comment'):
                d['reply_comment'] = json.loads(d['reply_comment'])
            results.append(d)
        return results

    def batch_delete(self, ids):
        if not ids:
            return 0
        placeholders = ','.join(['?'] * len(ids))
        now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
        cur = self.conn.execute(
            f"UPDATE tweets SET deleted=1, deleted_at=? WHERE id IN ({placeholders})",
            [now] + list(ids)
        )
        self.conn.commit()
        return cur.rowcount

    def restore_tweets(self, ids):
        if not ids:
            return 0
        placeholders = ','.join(['?'] * len(ids))
        cur = self.conn.execute(
            f"UPDATE tweets SET deleted=0, deleted_at=NULL WHERE id IN ({placeholders})",
            list(ids)
        )
        self.conn.commit()
        return cur.rowcount

    def get_tweet_ids(self):
        rows = self.conn.execute("SELECT mblogid FROM tweets WHERE deleted=0").fetchall()
        return [r[0] for r in rows]

    def stats(self):
        total = self.conn.execute("SELECT COUNT(*) FROM tweets").fetchone()[0]
        deleted_count = self.conn.execute(
            "SELECT COUNT(*) FROM tweets WHERE deleted=1"
        ).fetchone()[0]
        comments_count = self.conn.execute(
            "SELECT COUNT(*) FROM comments"
        ).fetchone()[0]
        return {
            'total_tweets': total,
            'deleted_tweets': deleted_count,
            'total_comments': comments_count,
        }
```

- [ ] **Step 4: 运行测试验证通过**

```bash
cd weibospider && PYTHONPATH=. python -m pytest ../tests/test_db.py -v
```
Expected: all 12 tests PASS

- [ ] **Step 5: Commit**

```bash
git add weibospider/db.py tests/test_db.py
git commit -m "feat: add SQLite database layer with CRUD operations"
```

---

### Task 3: 替换 pipelines.py 为 SQLite pipeline

**Files:**
- Modify: `weibospider/pipelines.py`

- [ ] **Step 1: 重写 pipelines.py**

```python
# weibospider/pipelines.py
import time
from db import TweetDB


class SqlitePipeline:
    """Scrapy pipeline: write items to SQLite."""

    db = None

    @classmethod
    def from_crawler(cls, crawler):
        pipeline = cls()
        cls.db = TweetDB()
        return pipeline

    def process_item(self, item, spider):
        item['crawl_time'] = int(time.time())

        if spider.name == 'tweet_spider_by_user_id':
            self.db.insert_tweet(item)
        elif spider.name == 'comment':
            self.db.insert_comment(item)

        return item
```

- [ ] **Step 2: Commit**

```bash
git add weibospider/pipelines.py
git commit -m "refactor: replace JSONL pipeline with SQLite pipeline"
```

---

### Task 4: 更新 settings.py

**Files:**
- Modify: `weibospider/settings.py`

- [ ] **Step 1: 修改 pipeline 配置**

将 settings.py 中第 28-30 行从：
```python
ITEM_PIPELINES = {
    'pipelines.JsonWriterPipeline': 300,
}
```
改为：
```python
ITEM_PIPELINES = {
    'pipelines.SqlitePipeline': 300,
}
```

- [ ] **Step 2: Commit**

```bash
git add weibospider/settings.py
git commit -m "config: switch to SQLite pipeline in Scrapy settings"
```

---

### Task 5: 改造 tweet_by_user_id.py — 支持外部参数、全量抓取

**Files:**
- Modify: `weibospider/spiders/tweet_by_user_id.py`

- [ ] **Step 1: 重写 spider**

```python
#!/usr/bin/env python
# encoding: utf-8
"""
采集指定用户的所有推文（改造版）
"""
import json
from scrapy import Spider
from scrapy.http import Request
from spiders.common import parse_tweet_info, parse_long_tweet


class TweetSpiderByUserID(Spider):
    name = "tweet_spider_by_user_id"

    def __init__(self, user_ids=None, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.user_ids = user_ids or ['1087770692']
        if isinstance(self.user_ids, str):
            self.user_ids = [self.user_ids]

    def start_requests(self):
        for user_id in self.user_ids:
            url = (
                f"https://weibo.com/ajax/statuses/searchProfile?"
                f"uid={user_id}&page=1&hasori=1&hastext=1&haspic=1"
                f"&hasvideo=1&hasmusic=1&hasret=1"
            )
            yield Request(url, callback=self.parse, meta={'user_id': user_id, 'page_num': 1})

    def parse(self, response, **kwargs):
        data = json.loads(response.text)
        tweets = data.get('data', {}).get('list', [])
        user_id = response.meta['user_id']

        for tweet in tweets:
            item = parse_tweet_info(tweet)
            item['user_id'] = user_id
            del item['user']
            if item['isLongText']:
                url = "https://weibo.com/ajax/statuses/longtext?id=" + item['mblogid']
                yield Request(url, callback=parse_long_tweet, meta={'item': item})
            else:
                yield item

        if tweets:
            page_num = response.meta['page_num']
            url = response.url.replace(f'page={page_num}', f'page={page_num + 1}')
            yield Request(url, callback=self.parse, meta={'user_id': user_id, 'page_num': page_num + 1})
```

- [ ] **Step 2: Commit**

```bash
git add weibospider/spiders/tweet_by_user_id.py
git commit -m "refactor: accept user_ids from spider args, remove time filter"
```

---

### Task 6: 改造 comment.py — 支持外部参数传递 tweet_id

**Files:**
- Modify: `weibospider/spiders/comment.py`

- [ ] **Step 1: 重写 spider**

```python
#!/usr/bin/env python
# encoding: utf-8
"""
微博评论数据采集（改造版）
"""
import json
from scrapy import Spider
from scrapy.http import Request
from spiders.common import parse_user_info, parse_time, url_to_mid


class CommentSpider(Spider):
    name = "comment"

    def __init__(self, tweet_ids=None, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.tweet_ids = tweet_ids or ['Mb15BDYR0']
        if isinstance(self.tweet_ids, str):
            self.tweet_ids = self.tweet_ids.split(',')

    def start_requests(self):
        for tweet_id in self.tweet_ids:
            mid = url_to_mid(tweet_id)
            url = (
                f"https://weibo.com/ajax/statuses/buildComments?"
                f"is_reload=1&id={mid}&is_show_bulletin=2&is_mix=0&count=20"
            )
            yield Request(url, callback=self.parse, meta={
                'source_url': url, 'tweet_id': str(mid)
            })

    def parse(self, response, **kwargs):
        data = json.loads(response.text)
        tweet_id = response.meta['tweet_id']

        for comment_info in data.get('data', []):
            item = self.parse_comment(comment_info)
            item['tweet_id'] = tweet_id
            yield item
            if 'more_info' in comment_info:
                url = (
                    f"https://weibo.com/ajax/statuses/buildComments?"
                    f"is_reload=1&id={comment_info['id']}"
                    f"&is_show_bulletin=2&is_mix=1&fetch_level=1&max_id=0&count=100"
                )
                yield Request(url, callback=self.parse, priority=20,
                              meta={'tweet_id': tweet_id})

        if data.get('max_id', 0) != 0 and 'fetch_level=1' not in response.url:
            url = response.meta['source_url'] + '&max_id=' + str(data['max_id'])
            yield Request(url, callback=self.parse, meta={
                'source_url': response.meta['source_url'],
                'tweet_id': tweet_id,
            })

    @staticmethod
    def parse_comment(data):
        item = dict()
        item['created_at'] = parse_time(data['created_at'])
        item['_id'] = data['id']
        item['like_counts'] = data['like_counts']
        item['ip_location'] = data.get('source', '')
        item['content'] = data['text_raw']
        item['comment_user'] = parse_user_info(data['user'])
        if 'reply_comment' in data:
            item['reply_comment'] = {
                '_id': data['reply_comment']['id'],
                'text': data['reply_comment']['text'],
                'user': parse_user_info(data['reply_comment']['user']),
            }
        return item
```

- [ ] **Step 2: Commit**

```bash
git add weibospider/spiders/comment.py
git commit -m "refactor: accept tweet_ids from spider args, add tweet_id to items"
```

---

### Task 7: 创建 scheduler.py — 定时调度

**Files:**
- Create: `weibospider/scheduler.py`

- [ ] **Step 1: 编写 scheduler.py**

```python
# weibospider/scheduler.py
import threading
import logging

from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.cron import CronTrigger

logger = logging.getLogger(__name__)


class CrawlScheduler:
    def __init__(self, crawl_func):
        self.crawl_func = crawl_func
        self._lock = threading.Lock()
        self._running = False
        self._last_result = None
        self._scheduler = BackgroundScheduler()
        self._scheduler.add_job(
            self._scheduled_crawl,
            CronTrigger(hour=2, minute=0),
            id='daily_crawl',
        )

    def start(self):
        self._scheduler.start()
        logger.info("Scheduler started, daily crawl at 02:00")

    def shutdown(self):
        self._scheduler.shutdown(wait=False)
        logger.info("Scheduler shutdown")

    def _scheduled_crawl(self):
        logger.info("Scheduled crawl triggered")
        self._execute()

    def manual_crawl(self):
        if self._running:
            return {'status': 'rejected', 'message': '已有抓取任务在运行'}
        # Run in background thread so API can return immediately
        t = threading.Thread(target=self._execute, daemon=True)
        t.start()
        return {'status': 'started', 'message': '抓取已启动'}

    def _execute(self):
        if not self._lock.acquire(blocking=False):
            logger.warning("Crawl already running, skip")
            return
        try:
            self._running = True
            self._last_result = None
            logger.info("Crawl started")
            result = self.crawl_func()
            self._last_result = result
            logger.info(f"Crawl finished: {result}")
        except Exception as e:
            self._last_result = {'error': str(e)}
            logger.error(f"Crawl failed: {e}")
        finally:
            self._running = False
            self._lock.release()

    @property
    def status(self):
        return {
            'running': self._running,
            'last_result': self._last_result,
        }
```

- [ ] **Step 2: Commit**

```bash
git add weibospider/scheduler.py
git commit -m "feat: add APScheduler-based daily crawl scheduler"
```

---

### Task 8: 创建 app.py — Flask API + 抓取逻辑

**Files:**
- Create: `weibospider/app.py`
- Create: `tests/test_app.py`

- [ ] **Step 1: 编写 app.py 的测试**

```python
# tests/test_app.py
import json
import os
import sys
import tempfile
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))


@pytest.fixture
def client():
    os.environ['SCRAPY_SETTINGS_MODULE'] = 'settings'
    # Use temp database
    fd, path = tempfile.mkstemp(suffix='.db')
    os.close(fd)

    from db import TweetDB
    import app as app_module
    app_module.DB = TweetDB(path)
    app_module.app.config['TESTING'] = True
    app_module.app.config['SCHEDULER_DISABLED'] = True

    c = app_module.app.test_client()
    yield c
    app_module.DB.close()
    os.unlink(path)


class TestAPI:
    def test_index_returns_html(self, client):
        rv = client.get('/')
        assert rv.status_code in (200, 404)  # 200 when index.html exists

    def test_tweets_empty(self, client):
        rv = client.get('/api/tweets?page=1&per_page=20')
        assert rv.status_code == 200
        data = json.loads(rv.data)
        assert data == []

    def test_tweets_with_data(self, client):
        import app as app_module
        for i in range(3):
            app_module.DB.insert_tweet({
                '_id': str(i), 'mblogid': f'Mb{i}', 'user_id': '1087770692',
                'content': f'tweet {i}', 'created_at': f'2024-01-01 0{i}:00:00',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })
        rv = client.get('/api/tweets?page=1&per_page=20')
        data = json.loads(rv.data)
        assert len(data) == 3

    def test_get_tweet_with_comments(self, client):
        import app as app_module
        app_module.DB.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        app_module.DB.insert_comment({
            '_id': 'c1', 'tweet_id': '1', 'content': 'nice',
            'created_at': '2024-01-01 11:00:00', 'like_counts': 0,
            'ip_location': '', 'comment_user': '{}',
            'reply_comment': None, 'crawl_time': 0,
        })
        rv = client.get('/api/tweets/1')
        data = json.loads(rv.data)
        assert data['tweet']['content'] == 'hello'
        assert len(data['comments']) == 1

    def test_single_delete(self, client):
        import app as app_module
        app_module.DB.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        rv = client.delete('/api/tweets/1')
        data = json.loads(rv.data)
        assert data['deleted'] == 1

    def test_batch_delete(self, client):
        import app as app_module
        for i in range(3):
            app_module.DB.insert_tweet({
                '_id': str(i), 'mblogid': f'Mb{i}', 'user_id': '1087770692',
                'content': f'tweet {i}', 'created_at': '2024-01-01 0{i}:00:00',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })
        rv = client.delete('/api/tweets/batch-delete',
                           json={'ids': ['0', '2']})
        data = json.loads(rv.data)
        assert data['deleted'] == 2

    def test_restore(self, client):
        import app as app_module
        app_module.DB.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        app_module.DB.batch_delete(['1'])
        rv = client.post('/api/tweets/restore', json={'ids': ['1']})
        data = json.loads(rv.data)
        assert data['restored'] == 1

    def test_stats(self, client):
        import app as app_module
        app_module.DB.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        rv = client.get('/api/stats')
        data = json.loads(rv.data)
        assert data['total_tweets'] == 1
        assert 'deleted_tweets' in data

    def test_crawl_trigger(self, client):
        rv = client.post('/api/crawl')
        data = json.loads(rv.data)
        assert data['status'] == 'started'

    def test_crawl_status(self, client):
        rv = client.get('/api/crawl/status')
        data = json.loads(rv.data)
        assert 'running' in data
```

- [ ] **Step 2: 验证测试失败**

```bash
cd weibospider && PYTHONPATH=. python -m pytest ../tests/test_app.py -v
```
Expected: FAIL — `ModuleNotFoundError: No module named 'app'` or similar

- [ ] **Step 3: 实现 app.py**

```python
# weibospider/app.py
import json
import logging
import os
import subprocess
import sys
import time

from flask import Flask, jsonify, request, send_from_directory

from db import TweetDB
from scheduler import CrawlScheduler

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

DB = None
SCHEDULER = None
app = Flask(__name__, static_folder='static')

# Configurable: the target user ID
USER_ID = os.environ.get('WEIBO_USER_ID', '1087770692')


def _crawl():
    """Execute crawl via subprocess to avoid Scrapy reactor restart issues."""
    script_dir = os.path.dirname(os.path.abspath(__file__))

    # Step 1: crawl tweets
    logger.info(f"Starting tweet crawl for user: {USER_ID}")
    subprocess.run([
        sys.executable, '-m', 'scrapy', 'crawl', 'tweet_spider_by_user_id',
        '-a', f'user_ids={USER_ID}',
        '-s', 'ITEM_PIPELINES={"pipelines.SqlitePipeline": 300}',
    ], cwd=script_dir, check=True)

    # Step 2: crawl comments for all tweets
    tweet_ids = DB.get_tweet_ids()
    logger.info(f"Crawling comments for {len(tweet_ids)} tweets")
    for mblogid in tweet_ids:
        subprocess.run([
            sys.executable, '-m', 'scrapy', 'crawl', 'comment',
            '-a', f'tweet_ids={mblogid}',
            '-s', 'ITEM_PIPELINES={"pipelines.SqlitePipeline": 300}',
        ], cwd=script_dir, check=True)
        time.sleep(0.5)  # small delay between comment crawls

    return {
        'tweets_total': len(tweet_ids),
        'status': 'completed',
    }


@app.route('/')
def index():
    return send_from_directory('static', 'index.html')


@app.route('/api/tweets')
def api_tweets():
    page = request.args.get('page', 1, type=int)
    per_page = request.args.get('per_page', 20, type=int)
    sort = request.args.get('sort', 'desc')
    deleted = request.args.get('deleted', 'exclude')

    tweets = DB.get_tweets(page=page, per_page=per_page, sort=sort, deleted=deleted)
    # Attach comment counts for each tweet
    for t in tweets:
        comments = DB.get_comments(t['id'])
        t['comments_list'] = comments
    return jsonify(tweets)


@app.route('/api/tweets/<tweet_id>')
def api_get_tweet(tweet_id):
    tweet = DB.get_tweet(tweet_id)
    if tweet is None:
        return jsonify({'error': 'tweet not found'}), 404
    comments = DB.get_comments(tweet_id)
    return jsonify({'tweet': tweet, 'comments': comments})


@app.route('/api/tweets/<tweet_id>', methods=['DELETE'])
def api_delete_tweet(tweet_id):
    count = DB.batch_delete([tweet_id])
    return jsonify({'deleted': count})


@app.route('/api/tweets/batch-delete', methods=['DELETE'])
def api_batch_delete():
    data = request.get_json()
    ids = data.get('ids', [])
    count = DB.batch_delete(ids)
    return jsonify({'deleted': count})


@app.route('/api/tweets/restore', methods=['POST'])
def api_restore():
    data = request.get_json()
    ids = data.get('ids', [])
    count = DB.restore_tweets(ids)
    return jsonify({'restored': count})


@app.route('/api/crawl', methods=['POST'])
def api_crawl():
    result = SCHEDULER.manual_crawl()
    return jsonify(result)


@app.route('/api/crawl/status')
def api_crawl_status():
    return jsonify(SCHEDULER.status)


@app.route('/api/stats')
def api_stats():
    return jsonify(DB.stats())


@app.errorhandler(404)
def not_found(e):
    return jsonify({'error': 'not found'}), 404


def create_app(db_path=None):
    global DB, SCHEDULER
    DB = TweetDB(db_path)
    if SCHEDULER is None and not app.config.get('SCHEDULER_DISABLED'):
        SCHEDULER = CrawlScheduler(_crawl)
        SCHEDULER.start()
    return app
```

- [ ] **Step 4: 运行测试验证通过**

```bash
cd weibospider && PYTHONPATH=. python -m pytest ../tests/test_app.py -v
```
Expected: 10 tests PASS

- [ ] **Step 5: Commit**

```bash
git add weibospider/app.py tests/test_app.py
git commit -m "feat: add Flask API with crawl integration"
```

---

### Task 9: 创建 SPA 前端 index.html

**Files:**
- Create: `weibospider/static/index.html`

- [ ] **Step 1: 创建 index.html**

```html
<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>微博管理器</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #1a1a2e; color: #cdd6f4; }
#app { max-width: 700px; margin: 0 auto; padding: 0 12px; }

/* Top bar */
.toolbar { position: sticky; top: 0; z-index: 100; background: #16162a; padding: 12px 16px; display: flex; justify-content: space-between; align-items: center; border-bottom: 1px solid #2a2a3f; gap: 8px; flex-wrap: wrap; }
.toolbar h1 { font-size: 16px; white-space: nowrap; }
.stats { font-size: 11px; color: #888; white-space: nowrap; }
.btn { padding: 6px 14px; border-radius: 6px; border: none; cursor: pointer; font-size: 12px; font-weight: bold; }
.btn-primary { background: #4a9eff; color: #fff; }
.btn-danger { background: #e74c3c; color: #fff; }
.btn-sm { padding: 4px 10px; font-size: 11px; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-ghost { background: transparent; border: 1px solid #555; color: #aaa; }
.toolbar-actions { display: flex; gap: 6px; align-items: center; }

/* Tabs */
.tabs { display: flex; gap: 0; margin: 12px 0 8px; border-bottom: 1px solid #2a2a3f; }
.tab { padding: 8px 16px; font-size: 13px; cursor: pointer; border: none; background: none; color: #888; border-bottom: 2px solid transparent; }
.tab.active { color: #4a9eff; border-bottom-color: #4a9eff; }

/* Cards */
.card { background: #1e1e35; border-radius: 10px; padding: 14px; border: 1px solid #333; display: flex; gap: 10px; }
.card+.card { margin-top: 10px; }
.card input[type=checkbox] { margin-top: 3px; accent-color: #4a9eff; width: 16px; height: 16px; flex-shrink: 0; cursor: pointer; }
.card-body { flex: 1; min-width: 0; }
.card-meta { color: #888; font-size: 11px; margin-bottom: 6px; display: flex; justify-content: space-between; }
.card-meta-left { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.card-meta-right { flex-shrink: 0; display: flex; gap: 8px; align-items: center; }
.btn-del { background: none; border: none; color: #888; cursor: pointer; font-size: 14px; padding: 2px; }
.btn-del:hover { color: #e74c3c; }
.card-content { line-height: 1.7; margin-bottom: 8px; color: #ddd; font-size: 14px; word-break: break-word; }
.card-images { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 8px; }
.card-images img { max-width: 180px; max-height: 180px; border-radius: 6px; object-fit: cover; background: #252540; }
.card-actions { display: flex; gap: 16px; color: #888; font-size: 12px; align-items: center; }

/* Comments */
.comments-toggle { color: #4a9eff; cursor: pointer; font-size: 12px; user-select: none; }
.comments-toggle:hover { text-decoration: underline; }
.comments-box { display: none; background: #15152a; border-radius: 8px; padding: 10px; margin-top: 8px; }
.comments-box.open { display: block; }
.comment { font-size: 12px; padding: 5px 0; line-height: 1.5; border-bottom: 1px solid #1e1e35; }
.comment:last-child { border-bottom: none; }
.comment-user { color: #f0a040; }
.comment-reply { margin-left: 16px; padding-left: 8px; border-left: 2px solid #444; color: #aaa; }
.comments-more { font-size: 11px; color: #666; text-align: center; padding-top: 4px; }

/* Loading */
.loader { text-align: center; padding: 30px; color: #888; font-size: 13px; }
.hidden { display: none; }

/* Toast */
.toast { position: fixed; top: 20px; left: 50%; transform: translateX(-50%); background: #2d5a2d; color: #fff; padding: 10px 20px; border-radius: 8px; font-size: 13px; z-index: 9999; opacity: 0; transition: opacity 0.3s; }
.toast.show { opacity: 1; }
.toast.error { background: #5a2d2d; }

/* Crawl progress */
.crawl-banner { background: #1e3a5f; padding: 8px 16px; text-align: center; font-size: 12px; color: #4a9eff; display: none; }
.crawl-banner.show { display: block; }

/* Select all */
.select-all { display: flex; align-items: center; gap: 8px; font-size: 12px; color: #888; padding: 8px 0; }
.select-all input { accent-color: #4a9eff; }
</style>
</head>
<body>
<div id="app">
  <div class="toolbar">
    <h1>&#x1f4e6; 微博管理器</h1>
    <span class="stats" id="stats-text">加载中...</span>
    <div class="toolbar-actions">
      <button class="btn btn-primary" id="btn-crawl">&#x1f504; 立即抓取</button>
      <button class="btn btn-danger" id="btn-batch-delete" disabled>&#x1f5d1; 删除选中</button>
    </div>
  </div>

  <div class="crawl-banner" id="crawl-banner">&#x23f3; 抓取进行中...</div>

  <div class="tabs">
    <button class="tab active" data-tab="active">&#x1f4dd; 微博列表</button>
    <button class="tab" data-tab="trash">&#x1f5d1; 回收站</button>
  </div>

  <div class="select-all" id="select-all-row">
    <input type="checkbox" id="select-all" onchange="toggleSelectAll()">
    <label for="select-all">全选/反选</label>
  </div>

  <div id="cards-container"></div>
  <div class="loader" id="loader">加载中...</div>
  <div class="loader hidden" id="end-marker">— 没有更多了 —</div>
</div>

<div class="toast" id="toast"></div>

<script>
let currentPage = 1;
let currentTab = 'active';
let selected = new Set();
let isLoading = false;
let hasMore = true;

document.getElementById('btn-crawl').addEventListener('click', triggerCrawl);
document.getElementById('btn-batch-delete').addEventListener('click', batchDelete);
document.querySelectorAll('.tab').forEach(t => t.addEventListener('click', e => switchTab(e.target.dataset.tab)));
window.addEventListener('scroll', onScroll);

loadTweets();
loadStats();
checkStatus();

function $(id) { return document.getElementById(id); }

async function loadTweets(reset) {
  if (isLoading || (!reset && !hasMore)) return;
  isLoading = true;
  if (reset) { currentPage = 1; hasMore = true; $('cards-container').innerHTML = ''; $('end-marker').classList.add('hidden'); selected.clear(); updateBatchBtn(); }
  $('loader').classList.remove('hidden');

  const deleted = currentTab === 'trash' ? 'only' : 'exclude';
  try {
    const resp = await fetch(`/api/tweets?page=${currentPage}&per_page=20&deleted=${deleted}`);
    const data = await resp.json();
    if (data.length === 0) { hasMore = false; $('end-marker').classList.remove('hidden'); }
    else {
      data.forEach(t => renderCard(t));
      currentPage++;
    }
  } catch(e) { console.error(e); }
  $('loader').classList.add('hidden');
  isLoading = false;
}

function renderCard(t) {
  const container = $('cards-container');
  const div = document.createElement('div');
  div.className = 'card';
  div.dataset.id = t.id;

  const pics = (typeof t.pic_urls === 'string' ? JSON.parse(t.pic_urls || '[]') : (t.pic_urls || []));
  const comments = t.comments_list || [];
  const commentCount = comments.length;

  div.innerHTML = `
    <input type="checkbox" data-id="${t.id}" onchange="toggleOne(this)" ${selected.has(t.id) ? 'checked' : ''}>
    <div class="card-body">
      <div class="card-meta">
        <span class="card-meta-left">${esc(t.created_at)} &bull; ${esc(t.source)} &bull; ${esc(t.ip_location)}</span>
        <span class="card-meta-right">
          <button class="btn-del" onclick="deleteOne('${t.id}')" title="删除">&#x2715;</button>
        </span>
      </div>
      <div class="card-content">${esc(t.content)}</div>
      ${pics.length ? `<div class="card-images">${pics.map(p => `<img src="${esc(p)}" loading="lazy">`).join('')}</div>` : ''}
      <div class="card-actions">
        <span>&#x1f501; ${t.reposts_count}</span>
        <span>&#x2764; ${t.attitudes_count}</span>
        <span class="comments-toggle" onclick="toggleComments(this, '${t.id}')">&#x1f4ac; ${commentCount} 条评论</span>
      </div>
      <div class="comments-box" id="comments-${t.id}">
        ${comments.slice(0, 5).map(c => `
          <div class="comment">
            <span class="comment-user">${esc((c.comment_user||{}).nick_name||'用户')}</span>: ${esc(c.content)}
            ${c.reply_comment ? `<div class="comment-reply"><span class="comment-user">${esc((c.reply_comment.user||{}).nick_name||'用户')}</span>: ${esc(c.reply_comment.text)}</div>` : ''}
          </div>
        `).join('')}
        ${commentCount > 5 ? `<div class="comments-more">... 还有 ${commentCount - 5} 条评论</div>` : ''}
      </div>
    </div>
  `;
  container.appendChild(div);
}

function toggleComments(el, id) {
  const box = $('comments-' + id);
  const isOpen = box.classList.contains('open');
  box.classList.toggle('open');
  el.textContent = (isOpen ? '\u{1f4ac}' : '\u{1f4ac}') + ' ' + (box.querySelectorAll('.comment').length > 5 ? '+ ' : '') + el.textContent.replace(/[\d]+ 条评论/, (box.querySelectorAll('.comment').length + ' 条评论'));

  // refresh comment count from data
  const commentsCount = box.querySelectorAll('.comment').length;
  const totalFromAPI = el.closest('.card-body').querySelectorAll('.comment').length;
  if (isOpen) {
    el.innerHTML = `&#x1f4ac; ${totalFromAPI} 条评论`;
  } else {
    el.innerHTML = `&#x1f4ac; ${totalFromAPI} 条评论`;
  }
}

function esc(s) {
  if (!s) return '';
  const d = document.createElement('div');
  d.textContent = String(s);
  return d.innerHTML;
}

function onScroll() {
  if (window.innerHeight + window.scrollY >= document.body.offsetHeight - 400) {
    loadTweets(false);
  }
}

function toggleSelectAll() {
  const checked = $('select-all').checked;
  document.querySelectorAll('.card input[type=checkbox]').forEach(cb => {
    cb.checked = checked;
    const id = cb.dataset.id;
    checked ? selected.add(id) : selected.delete(id);
  });
  updateBatchBtn();
}

function toggleOne(cb) {
  const id = cb.dataset.id;
  cb.checked ? selected.add(id) : selected.delete(id);
  updateBatchBtn();
  $('select-all').checked = selected.size === document.querySelectorAll('.card input[type=checkbox]').length;
}

function updateBatchBtn() {
  const btn = $('btn-batch-delete');
  btn.disabled = selected.size === 0;
  btn.textContent = selected.size > 0 ? `&#x1f5d1; 删除选中(${selected.size})` : '&#x1f5d1; 删除选中';
}

async function deleteOne(id) {
  if (!confirm('确认删除这条微博？')) return;
  try {
    await fetch(`/api/tweets/${id}`, { method: 'DELETE' });
    removeCard(id);
    toast('已删除');
    loadStats();
  } catch(e) { toast('删除失败', true); }
}

async function batchDelete() {
  if (selected.size === 0) return;
  if (!confirm(`确认删除选中的 ${selected.size} 条微博？`)) return;
  try {
    const ids = Array.from(selected);
    await fetch('/api/tweets/batch-delete', {
      method: 'DELETE',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({ ids })
    });
    ids.forEach(id => removeCard(id));
    selected.clear();
    updateBatchBtn();
    toast(`已删除 ${ids.length} 条`);
    loadStats();
  } catch(e) { toast('删除失败', true); }
}

function removeCard(id) {
  const card = document.querySelector(`.card[data-id="${id}"]`);
  if (card) card.remove();
}

async function triggerCrawl() {
  const btn = $('btn-crawl');
  btn.disabled = true;
  btn.textContent = '抓取中...';
  try {
    const resp = await fetch('/api/crawl', { method: 'POST' });
    const data = await resp.json();
    if (data.status === 'started') toast('抓取已启动，完成后刷新页面可查看新微博');
    else toast(data.message, true);
  } catch(e) { toast('请求失败', true); }
  btn.disabled = false;
  btn.textContent = '&#x1f504; 立即抓取';
}

async function checkStatus() {
  try {
    const resp = await fetch('/api/crawl/status');
    const data = await resp.json();
    if (data.running) {
      $('crawl-banner').classList.add('show');
      $('btn-crawl').disabled = true;
    } else {
      $('crawl-banner').classList.remove('show');
      $('btn-crawl').disabled = false;
      if (data.last_result) {
        loadTweets(true);
        loadStats();
      }
    }
  } catch(e) {}
  setTimeout(checkStatus, 5000);
}

async function loadStats() {
  try {
    const resp = await fetch('/api/stats');
    const data = await resp.json();
    $('stats-text').textContent = `共 ${data.total_tweets} 条 | 已删 ${data.deleted_tweets}`;
  } catch(e) {}
}

function switchTab(tab) {
  currentTab = tab;
  document.querySelectorAll('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === tab));
  $('select-all-row').style.display = tab === 'trash' ? 'none' : '';
  $('btn-batch-delete').style.display = tab === 'trash' ? 'none' : '';
  // In trash view, show restore button
  selected.clear();
  updateBatchBtn();
  loadTweets(true);
}

function toast(msg, isError) {
  const el = $('toast');
  el.textContent = msg;
  el.className = 'toast ' + (isError ? 'error' : '') + ' show';
  setTimeout(() => el.classList.remove('show'), 3000);
}
</script>
</body>
</html>
```

- [ ] **Step 2: Commit**

```bash
git add weibospider/static/index.html
git commit -m "feat: add SPA frontend with waterfall cards and batch delete"
```

---

### Task 10: 创建 run.py — 统一入口

**Files:**
- Create: `weibospider/run.py`

- [ ] **Step 1: 编写 run.py**

```python
#!/usr/bin/env python
# encoding: utf-8
"""微博管理器启动入口。
python run.py           # 默认端口 5000
python run.py --port 8080
"""
import argparse
import sys
import os

sys.path.insert(0, os.path.dirname(__file__))


def main():
    parser = argparse.ArgumentParser(description='微博管理器')
    parser.add_argument('--port', type=int, default=5000, help='Web 服务端口 (默认: 5000)')
    parser.add_argument('--host', default='0.0.0.0', help='监听地址 (默认: 0.0.0.0)')
    args = parser.parse_args()

    from app import create_app
    app = create_app()
    print(f"微博管理器已启动: http://localhost:{args.port}")
    app.run(host=args.host, port=args.port, debug=False, threaded=True)


if __name__ == '__main__':
    main()
```

- [ ] **Step 2: Commit**

```bash
git add weibospider/run.py
git commit -m "feat: add unified web entry point"
```

---

### Task 11: 集成测试 — 确保整体可运行

- [ ] **Step 1: 运行全部单元测试**

```bash
cd weibospider && PYTHONPATH=. python -m pytest ../tests/ -v
```
Expected: all 22 tests PASS

- [ ] **Step 2: 添加 .gitignore 忽略数据文件**

```bash
echo ".superpowers/" >> .gitignore
echo "*.db" >> .gitignore
echo "__pycache__/" >> .gitignore
echo ".pytest_cache/" >> .gitignore
```

- [ ] **Step 3: Commit**

```bash
git add .gitignore
git commit -m "chore: add .gitignore for db and cache files"
```
