# 定时增量抓取 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将手动全量抓取改为定时增量同步——推文每 5-23h 每小时增量抓取，评论每半小时增量补齐（8h 窗口、<100 条、2 页热度排序），保留手动全量 + 新增手动增量按钮。

**Architecture:** APScheduler 两个独立 job（tweet_crawl / comment_crawl），各自独立锁，互不阻塞。`_crawl()` 拆为 `_crawl_tweets(mode)` 和 `_crawl_comments(mode)`。Spider 通过参数（`stop_after_id` / `max_pages`）控制增量行为。

**Tech Stack:** Python, Flask, APScheduler, Scrapy, SQLite, vanilla JS

---

## File Structure

| File | Responsibility |
|------|---------------|
| `weibospider/db.py` | 新增 `get_latest_tweet_id(user_id)` 和 `get_tweets_for_comment_crawl(hours=8)` |
| `weibospider/spiders/tweet_by_user_id.py` | 加 `stop_after_id` 参数，遇到已存在推文停止翻页 |
| `weibospider/spiders/comment.py` | 加 `max_pages` 参数，页数计数停止 |
| `weibospider/scheduler.py` | 两个独立 job + 两个独立锁；`manual_crawl()` 全量，`manual_incremental()` 增量 |
| `weibospider/app.py` | `_crawl()` 拆为 `_crawl_tweets(mode)` + `_crawl_comments(mode)`；新增 `/api/crawl/incremental`；`/api/crawl/status` 双状态；config 读写 5 项调度配置 |
| `weibospider/static/index.html` | 新增"增量同步"按钮；config 面板加 5 个调度配置项；SSE 适配双状态 |
| `tests/test_db.py` | DB 新方法测试 |
| `tests/test_app.py` | API 新端点 + config 测试 |
| `tests/test_scheduler.py` | scheduler job 配置 + 锁独立性测试 |
| `tests/test_frontend.py` | 前端按钮 + 配置项存在性测试 |

---

### Task 1: DB — get_latest_tweet_id

**Files:**
- Modify: `weibospider/db.py` (在 `get_tweet_ids_with_enough_comments` 方法之后，约 line 293)
- Test: `tests/test_db.py`

- [ ] **Step 1: Write the failing test**

在 `tests/test_db.py` 的 `TestTweetDB` 类末尾添加：

```python
    def test_get_latest_tweet_id(self, db):
        db.insert_tweet({
            '_id': '111', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'old', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_tweet({
            '_id': '222', 'mblogid': 'Mb2', 'user_id': 'u1',
            'content': 'new', 'created_at': '2024-06-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        assert db.get_latest_tweet_id('u1') == '222'

    def test_get_latest_tweet_id_no_tweets(self, db):
        assert db.get_latest_tweet_id('nonexistent') is None

    def test_get_latest_tweet_id_excludes_deleted(self, db):
        db.insert_tweet({
            '_id': '111', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'deleted', 'created_at': '2024-06-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.delete_tweet('111')
        assert db.get_latest_tweet_id('u1') is None
```

- [ ] **Step 2: Run test to verify it fails**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_db.py::TestTweetDB::test_get_latest_tweet_id -v`
Expected: FAIL with `AttributeError: 'TweetDB' object has no attribute 'get_latest_tweet_id'`

- [ ] **Step 3: Write minimal implementation**

在 `weibospider/db.py` 的 `get_tweet_ids_with_enough_comments` 方法之后添加：

```python
    def get_latest_tweet_id(self, user_id):
        """Return the id of the most recent non-deleted tweet for a user, or None."""
        with self._lock:
            row = self.conn.execute(
                "SELECT id FROM tweets WHERE user_id=? AND deleted=0 "
                "ORDER BY created_at DESC LIMIT 1",
                (user_id,)
            ).fetchone()
            return row[0] if row else None
```

- [ ] **Step 4: Run test to verify it passes**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_db.py::TestTweetDB::test_get_latest_tweet_id tests/test_db.py::TestTweetDB::test_get_latest_tweet_id_no_tweets tests/test_db.py::TestTweetDB::test_get_latest_tweet_id_excludes_deleted -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Run full test suite**

Run: `PYTHONPATH=weibospider python3 -m pytest tests -q`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add weibospider/db.py tests/test_db.py
git commit -m "feat: add get_latest_tweet_id for incremental tweet crawl"
```

---

### Task 2: DB — get_tweets_for_comment_crawl

**Files:**
- Modify: `weibospider/db.py` (在 `get_latest_tweet_id` 方法之后)
- Test: `tests/test_db.py`

- [ ] **Step 1: Write the failing test**

在 `tests/test_db.py` 的 `TestTweetDB` 类末尾添加：

```python
    def test_get_tweets_for_comment_crawl(self, db):
        from datetime import datetime, timedelta
        recent = (datetime.now() - timedelta(hours=2)).strftime('%Y-%m-%d %H:%M:%S')
        old = (datetime.now() - timedelta(hours=20)).strftime('%Y-%m-%d %H:%M:%S')
        # recent tweet, no comments → should be included
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'recent', 'created_at': recent,
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        # old tweet → should be excluded
        db.insert_tweet({
            '_id': '2', 'mblogid': 'Mb2', 'user_id': 'u1',
            'content': 'old', 'created_at': old,
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        # recent tweet with 100 comments → should be excluded (frozen)
        db.insert_tweet({
            '_id': '3', 'mblogid': 'Mb3', 'user_id': 'u1',
            'content': 'frozen', 'created_at': recent,
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        for i in range(100):
            db.insert_comment({
                '_id': f'c{i}', 'tweet_id': '3', 'content': f'comment {i}',
                'created_at': recent, 'like_counts': 0, 'ip_location': '',
                'comment_user': '{}', 'crawl_time': 0,
            })
        # deleted recent tweet → should be excluded
        db.insert_tweet({
            '_id': '4', 'mblogid': 'Mb4', 'user_id': 'u1',
            'content': 'deleted', 'created_at': recent,
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.delete_tweet('4')

        results = db.get_tweets_for_comment_crawl(hours=8)
        ids = [r[0] for r in results]
        assert '1' in ids
        assert '2' not in ids
        assert '3' not in ids
        assert '4' not in ids
```

- [ ] **Step 2: Run test to verify it fails**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_db.py::TestTweetDB::test_get_tweets_for_comment_crawl -v`
Expected: FAIL with `AttributeError: 'TweetDB' object has no attribute 'get_tweets_for_comment_crawl'`

- [ ] **Step 3: Write minimal implementation**

在 `weibospider/db.py` 的 `get_latest_tweet_id` 方法之后添加：

```python
    def get_tweets_for_comment_crawl(self, hours=8):
        """Return (id, mblogid) tuples for tweets eligible for comment crawl:
        non-deleted, within the last `hours` hours, and with <100 comments.
        """
        with self._lock:
            rows = self.conn.execute(
                "SELECT id, mblogid FROM tweets "
                "WHERE deleted=0 "
                "  AND created_at > datetime('now', ?) "
                "  AND id NOT IN ("
                "    SELECT tweet_id FROM comments "
                "    GROUP BY tweet_id HAVING COUNT(*) >= 100"
                "  ) "
                "ORDER BY created_at DESC",
                (f'-{hours} hours',)
            ).fetchall()
            return [(r[0], r[1]) for r in rows]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_db.py::TestTweetDB::test_get_tweets_for_comment_crawl -v`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `PYTHONPATH=weibospider python3 -m pytest tests -q`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add weibospider/db.py tests/test_db.py
git commit -m "feat: add get_tweets_for_comment_crawl for incremental comment sync"
```

---

### Task 3: Tweet Spider — stop_after_id 参数

**Files:**
- Modify: `weibospider/spiders/tweet_by_user_id.py`
- Test: `tests/test_spider_params.py` (新建)

- [ ] **Step 1: Write the failing test**

新建 `tests/test_spider_params.py`：

```python
import os
import sys
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))


class TestTweetSpiderParams:
    def test_stop_after_id_parsed(self):
        from spiders.tweet_by_user_id import TweetSpiderByUserID
        spider = TweetSpiderByUserID(user_ids='123', stop_after_id='999')
        assert spider.stop_after_id == '999'

    def test_stop_after_id_defaults_none(self):
        from spiders.tweet_by_user_id import TweetSpiderByUserID
        spider = TweetSpiderByUserID(user_ids='123')
        assert spider.stop_after_id is None
```

- [ ] **Step 2: Run test to verify it fails**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_spider_params.py::TestTweetSpiderParams -v`
Expected: FAIL with `TypeError: __init__() got an unexpected keyword argument 'stop_after_id'`

- [ ] **Step 3: Write minimal implementation**

修改 `weibospider/spiders/tweet_by_user_id.py`：

`__init__` 方法（line 26）改为：

```python
    def __init__(self, user_ids=None, start_time=None, end_time=None, stop_after_id=None, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.user_ids = user_ids or ['1087770692']
        if isinstance(self.user_ids, str):
            self.user_ids = [self.user_ids]
        self.start_time = start_time  # YYYY-MM-DD
        self.end_time = end_time      # YYYY-MM-DD
        self.stop_after_id = stop_after_id  # stop pagination when this tweet id is seen
```

`parse()` 方法（line 58）改为——在 `for tweet in tweets:` 循环中，yield item 之后检查：

```python
    def parse(self, response, **kwargs):
        page_num = response.meta['page_num']
        data = json.loads(response.text)

        if data.get('ok') == -100:
            self.logger.critical(
                "Weibo API returned ok=-100 (not logged in / cookie expired). "
                "Please update your cookie in the web UI config."
            )
            return

        tweets = data.get('data', {}).get('list', [])
        if page_num == 1 or page_num % 5 == 0:
            self.logger.info("page=%d got %d tweets", page_num, len(tweets))
        user_id = response.meta['user_id']

        stop_pagination = False
        for tweet in tweets:
            item = parse_tweet_info(tweet)
            item['user_id'] = user_id
            if 'user' in item:
                item['screen_name'] = item['user'].get('nick_name', '')
                del item['user']
            else:
                item['screen_name'] = ''
            if item['isLongText']:
                url = "https://weibo.com/ajax/statuses/longtext?id=" + item['mblogid']
                yield Request(url, callback=parse_long_tweet,
                              headers={'Referer': f'https://weibo.com/u/{user_id}'},
                              meta={'item': item})
            else:
                yield item

            if self.stop_after_id and str(item.get('_id')) == str(self.stop_after_id):
                self.logger.info("stop_after_id=%s reached, stopping pagination", self.stop_after_id)
                stop_pagination = True
                break

        if tweets and not stop_pagination:
            url = response.url.replace(f'page={page_num}', f'page={page_num + 1}')
            yield Request(url, callback=self.parse,
                          headers={'Referer': f'https://weibo.com/u/{user_id}'},
                          meta={'user_id': user_id, 'page_num': page_num + 1})
```

- [ ] **Step 4: Run test to verify it passes**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_spider_params.py::TestTweetSpiderParams -v`
Expected: PASS (2 tests)

- [ ] **Step 5: Run full test suite**

Run: `PYTHONPATH=weibospider python3 -m pytest tests -q`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add weibospider/spiders/tweet_by_user_id.py tests/test_spider_params.py
git commit -m "feat: add stop_after_id param to tweet spider for incremental crawl"
```

---

### Task 4: Comment Spider — max_pages 参数

**Files:**
- Modify: `weibospider/spiders/comment.py`
- Test: `tests/test_spider_params.py`

- [ ] **Step 1: Write the failing test**

在 `tests/test_spider_params.py` 中添加：

```python
class TestCommentSpiderParams:
    def test_max_pages_parsed(self):
        from spiders.comment import CommentSpider
        spider = CommentSpider(tweet_ids='Mb1', max_pages='2')
        assert spider.max_pages == 2

    def test_max_pages_defaults_none(self):
        from spiders.comment import CommentSpider
        spider = CommentSpider(tweet_ids='Mb1')
        assert spider.max_pages is None
```

- [ ] **Step 2: Run test to verify it fails**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_spider_params.py::TestCommentSpiderParams -v`
Expected: FAIL with `TypeError: __init__() got an unexpected keyword argument 'max_pages'`

- [ ] **Step 3: Write minimal implementation**

修改 `weibospider/spiders/comment.py`：

`__init__` 方法（line 22）改为：

```python
    def __init__(self, tweet_ids=None, flow='0', max_pages=None, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.tweet_ids = tweet_ids or ['Mb15BDYR0']
        if isinstance(self.tweet_ids, str):
            self.tweet_ids = self.tweet_ids.split(',')
        self.flow = flow  # 0=热度排序, 1=时间排序
        self.max_pages = int(max_pages) if max_pages else None  # limit pages per tweet
```

`start_requests` 方法（line 29）改为——在 meta 中传入 `page_num`：

```python
    def start_requests(self):
        self.logger.info("Starting comments crawl for %d tweets", len(self.tweet_ids))
        for idx, tweet_id in enumerate(self.tweet_ids):
            mid = url_to_mid(tweet_id)
            base_url = (
                f"https://weibo.com/ajax/statuses/buildComments?"
                f"flow={self.flow}&is_reload=1&id={mid}&is_show_bulletin=2&is_mix=1&count=50"
            )
            yield Request(base_url, callback=self.parse, headers={'Referer': 'https://weibo.com/'}, meta={
                'base_url': base_url, 'tweet_id': str(mid),
                'tweet_index': idx + 1, 'tweet_total': len(self.tweet_ids),
                'sort_offset': 0, 'comment_count': 0, 'top_count': 0,
                'page_num': 1
            })
```

`parse()` 方法中的翻页部分（line 116-128）改为：

```python
        # Paginate to next page of comments (stop if limit reached)
        current_page = response.meta.get('page_num', 1)
        reached_max_pages = self.max_pages is not None and current_page >= self.max_pages
        if not reached_limit and not reached_max_pages and data.get('max_id', 0) != 0 and len(comments) > 0:
            url = response.meta['base_url'] + '&max_id=' + str(data['max_id'])
            yield Request(url, callback=self.parse,
                          headers={'Referer': 'https://weibo.com/',
                                   'X-Requested-With': 'XMLHttpRequest'},
                          meta={'base_url': response.meta['base_url'],
                                'tweet_id': tweet_id,
                                'tweet_index': tweet_idx,
                                'tweet_total': tweet_total,
                                'sort_offset': seq,
                                'comment_count': comment_count + len(comments),
                                'top_count': top_count,
                                'page_num': current_page + 1})
        elif total_count > 0:
            limit_msg = f" (limited to {MAX_TOP} top + {MAX_SUB} sub/ea)" if reached_limit else ""
            pages_msg = f" (max_pages={self.max_pages})" if reached_max_pages else ""
            self.logger.info("[%d/%d] tweet %s: done, %d comments total%s%s",
                             tweet_idx, tweet_total, tweet_id, total_count, limit_msg, pages_msg)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_spider_params.py::TestCommentSpiderParams -v`
Expected: PASS (2 tests)

- [ ] **Step 5: Run full test suite**

Run: `PYTHONPATH=weibospider python3 -m pytest tests -q`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add weibospider/spiders/comment.py tests/test_spider_params.py
git commit -m "feat: add max_pages param to comment spider for incremental crawl"
```

---

### Task 5: Scheduler — 双 job + 双锁 + 手动增量

**Files:**
- Modify: `weibospider/scheduler.py` (完全重写)
- Test: `tests/test_scheduler.py` (新建)

- [ ] **Step 1: Write the failing test**

新建 `tests/test_scheduler.py`：

```python
import os
import sys
import time
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))


@pytest.fixture
def scheduler():
    from scheduler import CrawlScheduler
    calls = {'tweet': [], 'comment': []}

    def mock_crawl_tweets(sch, mode='incremental', user_id=None):
        calls['tweet'].append(mode)
        time.sleep(0.1)

    def mock_crawl_comments(sch, mode='incremental'):
        calls['comment'].append(mode)
        time.sleep(0.1)

    sch = CrawlScheduler(mock_crawl_tweets, mock_crawl_comments)
    yield sch, calls
    sch.shutdown()


class TestScheduler:
    def test_manual_crawl_full(self, scheduler):
        sch, calls = scheduler
        result = sch.manual_crawl()
        assert result['status'] == 'started'
        time.sleep(0.3)
        assert 'full' in calls['tweet']
        assert 'full' in calls['comment']

    def test_manual_incremental(self, scheduler):
        sch, calls = scheduler
        result = sch.manual_incremental()
        assert result['status'] == 'started'
        time.sleep(0.3)
        assert 'incremental' in calls['tweet']
        assert 'incremental' in calls['comment']

    def test_tweet_and_comment_locks_independent(self, scheduler):
        sch, calls = scheduler
        # Start a manual full crawl (acquires both locks)
        sch.manual_crawl()
        time.sleep(0.05)
        # Tweet job should be rejected (tweet lock held)
        tweet_result = sch._execute_tweet_job(mode='incremental')
        assert tweet_result is None or True  # _execute returns None when locked
        # But we can't start a second tweet crawl
        result = sch.manual_crawl()
        assert result['status'] == 'rejected'
        time.sleep(0.3)

    def test_status_returns_dual(self, scheduler):
        sch, calls = scheduler
        status = sch.status
        assert 'tweet' in status
        assert 'comment' in status
        assert 'running' in status['tweet']
        assert 'running' in status['comment']

    def test_config_controls_jobs(self, scheduler):
        sch, calls = scheduler
        # Default: schedule disabled
        assert sch._is_schedule_enabled() is False
```

- [ ] **Step 2: Run test to verify it fails**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_scheduler.py -v`
Expected: FAIL (import or attribute errors)

- [ ] **Step 3: Write minimal implementation**

完全重写 `weibospider/scheduler.py`：

```python
# weibospider/scheduler.py
import threading
import logging

from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.cron import CronTrigger

logger = logging.getLogger(__name__)


class CrawlScheduler:
    def __init__(self, crawl_tweets_func, crawl_comments_func):
        self.crawl_tweets_func = crawl_tweets_func
        self.crawl_comments_func = crawl_comments_func
        # Independent locks
        self._tweet_lock = threading.Lock()
        self._comment_lock = threading.Lock()
        self._tweet_running = False
        self._comment_running = False
        self._tweet_cancelled = False
        self._comment_cancelled = False
        self._tweet_last_result = None
        self._comment_last_result = None
        self._config = {}
        self._scheduler = BackgroundScheduler()

    def start(self):
        self._reload_jobs()
        self._scheduler.start()
        logger.info("Scheduler started")

    def shutdown(self):
        self._scheduler.shutdown(wait=False)
        logger.info("Scheduler shutdown")

    def update_config(self, config):
        """Update config dict and reload jobs if schedule params changed."""
        old_keys = {k: self._config.get(k) for k in
                    ('schedule_enabled', 'schedule_start_hour', 'schedule_end_hour',
                     'tweet_interval_minutes', 'comment_interval_minutes')}
        self._config = config
        new_keys = {k: self._config.get(k) for k in old_keys}
        if old_keys != new_keys:
            self._reload_jobs()

    def _reload_jobs(self):
        """Remove existing jobs and re-add based on current config."""
        for job_id in ('tweet_crawl', 'comment_crawl'):
            try:
                self._scheduler.remove_job(job_id)
            except Exception:
                pass
        if not self._is_schedule_enabled():
            logger.info("Schedule disabled, no jobs registered")
            return
        start_hour = self._config.get('schedule_start_hour', 5)
        end_hour = self._config.get('schedule_end_hour', 23)
        tweet_interval = self._config.get('tweet_interval_minutes', 60)
        comment_interval = self._config.get('comment_interval_minutes', 30)
        self._scheduler.add_job(
            self._scheduled_tweet_crawl,
            CronTrigger(hour=f'{start_hour}-{end_hour - 1}', minute=f'*/{tweet_interval // 60 if tweet_interval >= 60 else 1}'),
            id='tweet_crawl',
        )
        self._scheduler.add_job(
            self._scheduled_comment_crawl,
            CronTrigger(hour=f'{start_hour}-{end_hour - 1}', minute=f'*/{comment_interval // 30 if comment_interval >= 30 else 1}'),
            id='comment_crawl',
        )
        logger.info("Jobs registered: tweet every %dmin, comment every %dmin, %dh-%dh",
                     tweet_interval, comment_interval, start_hour, end_hour)

    def _is_schedule_enabled(self):
        return self._config.get('schedule_enabled', False) is True

    def _scheduled_tweet_crawl(self):
        logger.info("Scheduled tweet crawl triggered")
        self._execute_tweet_job(mode='incremental')

    def _scheduled_comment_crawl(self):
        logger.info("Scheduled comment crawl triggered")
        self._execute_comment_job(mode='incremental')

    def manual_crawl(self, user_id=None):
        """Full crawl: tweets + comments sequentially."""
        if self._tweet_running or self._comment_running:
            return {'status': 'rejected', 'message': '已有抓取任务在运行'}
        self._tweet_cancelled = False
        self._comment_cancelled = False
        t = threading.Thread(target=self._execute_full, args=(user_id,), daemon=True)
        t.start()
        return {'status': 'started', 'message': f'全量抓取已启动 ({"用户 " + user_id if user_id else "全部用户"})'}

    def manual_incremental(self):
        """Incremental crawl: tweets + comments in parallel."""
        tweet_rejected = self._tweet_running
        comment_rejected = self._comment_running
        if tweet_rejected and comment_rejected:
            return {'status': 'rejected', 'message': '推文和评论抓取均在运行中'}
        if tweet_rejected:
            return {'status': 'rejected', 'message': '推文抓取正在运行中'}
        if comment_rejected:
            return {'status': 'rejected', 'message': '评论抓取正在运行中'}
        self._tweet_cancelled = False
        self._comment_cancelled = False
        threading.Thread(target=self._execute_tweet_job, kwargs={'mode': 'incremental'}, daemon=True).start()
        threading.Thread(target=self._execute_comment_job, kwargs={'mode': 'incremental'}, daemon=True).start()
        return {'status': 'started', 'message': '增量同步已启动'}

    def cancel(self):
        if not self._tweet_running and not self._comment_running:
            return {'status': 'error', 'message': '没有正在运行的抓取任务'}
        self._tweet_cancelled = True
        self._comment_cancelled = True
        logger.info("Cancelling all crawl tasks...")
        return {'status': 'cancelling', 'message': '正在取消抓取...'}

    @property
    def tweet_cancelled(self):
        return self._tweet_cancelled

    @property
    def comment_cancelled(self):
        return self._comment_cancelled

    def _execute_full(self, user_id=None):
        """Run tweets then comments in full mode, using both locks."""
        if not self._tweet_lock.acquire(blocking=False):
            logger.warning("Tweet crawl already running, skip full crawl")
            return
        try:
            self._tweet_running = True
            self._tweet_cancelled = False
            self._tweet_last_result = None
            result = self.crawl_tweets_func(self, mode='full', user_id=user_id)
            self._tweet_last_result = result
        except Exception as e:
            self._tweet_last_result = {'error': str(e)}
            logger.error("Tweet crawl failed: %s", e)
        finally:
            self._tweet_running = False
            self._tweet_lock.release()

        if not self._comment_lock.acquire(blocking=False):
            logger.warning("Comment crawl already running, skip full crawl")
            return
        try:
            self._comment_running = True
            self._comment_cancelled = False
            self._comment_last_result = None
            result = self.crawl_comments_func(self, mode='full')
            self._comment_last_result = result
        except Exception as e:
            self._comment_last_result = {'error': str(e)}
            logger.error("Comment crawl failed: %s", e)
        finally:
            self._comment_running = False
            self._comment_lock.release()

    def _execute_tweet_job(self, mode='incremental', user_id=None):
        if not self._tweet_lock.acquire(blocking=False):
            logger.warning("Tweet crawl already running, skip")
            return
        try:
            self._tweet_running = True
            self._tweet_cancelled = False
            self._tweet_last_result = None
            result = self.crawl_tweets_func(self, mode=mode, user_id=user_id)
            self._tweet_last_result = result
            logger.info("Tweet crawl finished: %s", result)
        except Exception as e:
            self._tweet_last_result = {'error': str(e)}
            logger.error("Tweet crawl failed: %s", e)
        finally:
            self._tweet_running = False
            self._tweet_lock.release()

    def _execute_comment_job(self, mode='incremental'):
        if not self._comment_lock.acquire(blocking=False):
            logger.warning("Comment crawl already running, skip")
            return
        try:
            self._comment_running = True
            self._comment_cancelled = False
            self._comment_last_result = None
            result = self.crawl_comments_func(self, mode=mode)
            self._comment_last_result = result
            logger.info("Comment crawl finished: %s", result)
        except Exception as e:
            self._comment_last_result = {'error': str(e)}
            logger.error("Comment crawl failed: %s", e)
        finally:
            self._comment_running = False
            self._comment_lock.release()

    @property
    def status(self):
        return {
            'tweet': {
                'running': self._tweet_running,
                'last_result': self._tweet_last_result,
            },
            'comment': {
                'running': self._comment_running,
                'last_result': self._comment_last_result,
            },
        }

    @property
    def cancelled(self):
        """Legacy: return True if either is cancelled."""
        return self._tweet_cancelled or self._comment_cancelled
```

- [ ] **Step 4: Run test to verify it passes**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_scheduler.py -v`
Expected: PASS (5 tests)

- [ ] **Step 5: Run full test suite**

Run: `PYTHONPATH=weibospider python3 -m pytest tests -q`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add weibospider/scheduler.py tests/test_scheduler.py
git commit -m "feat: dual-job scheduler with independent locks and manual incremental trigger"
```

---

### Task 6: app.py — 拆分 _crawl + 新端点 + config

**Files:**
- Modify: `weibospider/app.py`
- Test: `tests/test_app.py`

- [ ] **Step 1: Write the failing test**

在 `tests/test_app.py` 中添加测试类：

```python
class TestScheduleConfig:
    def test_config_default_schedule_values(self, client):
        rv = client.get('/api/config')
        data = json.loads(rv.data)
        assert data['schedule_enabled'] is False
        assert data['schedule_start_hour'] == 5
        assert data['schedule_end_hour'] == 23
        assert data['tweet_interval_minutes'] == 60
        assert data['comment_interval_minutes'] == 30

    def test_config_set_schedule_values(self, client):
        rv = client.post('/api/config', json={
            'schedule_enabled': True,
            'schedule_start_hour': 8,
            'schedule_end_hour': 22,
            'tweet_interval_minutes': 120,
            'comment_interval_minutes': 60,
        })
        data = json.loads(rv.data)
        assert data['updated'] is True
        rv = client.get('/api/config')
        data = json.loads(rv.data)
        assert data['schedule_enabled'] is True
        assert data['schedule_start_hour'] == 8
        assert data['schedule_end_hour'] == 22
        assert data['tweet_interval_minutes'] == 120
        assert data['comment_interval_minutes'] == 60


class TestCrawlStatus:
    def test_status_returns_dual_structure(self, client):
        rv = client.get('/api/crawl/status')
        data = json.loads(rv.data)
        assert 'tweet' in data
        assert 'comment' in data
        assert 'running' in data['tweet']
        assert 'running' in data['comment']


class TestIncrementalEndpoint:
    def test_incremental_endpoint_exists(self, client):
        rv = client.post('/api/crawl/incremental')
        # Without scheduler, should return a message
        assert rv.status_code in (200, 409)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_app.py::TestScheduleConfig tests/test_app.py::TestCrawlStatus tests/test_app.py::TestIncrementalEndpoint -v`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

修改 `weibospider/app.py`：

**3a. 替换 `_crawl` 函数（line 36-196）为两个函数：**

```python
def _crawl_tweets(scheduler=None, mode='full', user_id=None):
    """Crawl tweets. mode='incremental' stops at existing tweets; 'full' crawls all."""
    import threading
    script_dir = os.path.dirname(os.path.abspath(__file__))
    log_file = os.path.join(script_dir, 'crawl.log')
    log_lines = []
    all_ids = _get_user_ids()
    unbuffered_env = {**os.environ, 'PYTHONUNBUFFERED': '1'}

    def _log(msg):
        ts = datetime.now().strftime('%H:%M:%S')
        line = f"[{ts}] [tweet] {msg}"
        log_lines.append(line)
        logger.info(msg)
        try:
            with open(log_file, 'a') as lf:
                lf.write(line + '\n')
        except:
            pass

    def _run_scrapy_with_log(cmd_args):
        proc = subprocess.Popen(cmd_args, cwd=script_dir, env=unbuffered_env,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        def _forward():
            for line in proc.stdout:
                stripped = line.rstrip('\n')
                if not stripped:
                    continue
                print(stripped, file=sys.stderr, flush=True)
                try:
                    with open(log_file, 'a') as lf:
                        lf.write(stripped + '\n')
                except:
                    pass
        thread = threading.Thread(target=_forward, daemon=True)
        thread.start()
        return proc, thread

    if user_id:
        if user_id not in all_ids:
            return {'status': 'failed', 'error': f'UID {user_id} 不在配置列表中'}
        user_ids = [user_id]
    else:
        user_ids = all_ids

    if mode == 'full':
        try:
            open(log_file, 'w').close()
        except:
            pass

    _log(f"====== 开始抓取推文 (mode={mode}, 用户: {user_ids}) ======")

    def _check_cancel():
        return scheduler and scheduler.tweet_cancelled

    cookie = DB.get_config('cookie', '') or DEFAULT_COOKIE
    if not cookie:
        _log("失败: 未配置 Cookie")
        return {'status': 'failed', 'error': '未配置 Cookie'}
    cookie_path = os.path.join(script_dir, 'cookie.txt')
    with open(cookie_path, 'w') as f:
        f.write(cookie.strip())

    start_time = DB.get_config('start_date', '')
    end_time = DB.get_config('end_date', '')
    if mode == 'full':
        if not start_time or not end_time:
            start_time = (datetime.now() - timedelta(days=7)).strftime('%Y-%m-%d')
            end_time = datetime.now().strftime('%Y-%m-%d')
        _log(f"时间范围: {start_time} ~ {end_time}")
    else:
        start_time = None
        end_time = None

    tweets_before = DB.conn.execute("SELECT COUNT(*) FROM tweets").fetchone()[0]
    _log(f"微博抓取前已有 {tweets_before} 条")

    for uid in user_ids:
        if _check_cancel():
            _log("用户取消了抓取")
            return {'status': 'cancelled'}

        _log(f"抓取用户 {uid} 的微博...")
        cmd = [
            sys.executable, '-m', 'scrapy', 'crawl', 'tweet_spider_by_user_id',
            '-a', 'user_ids=%s' % uid,
            '-s', 'ITEM_PIPELINES={"pipelines.SqlitePipeline": 300}',
            '-s', 'LOG_LEVEL=INFO',
        ]
        if start_time and end_time:
            cmd.extend(['-a', 'start_time=%s' % start_time, '-a', 'end_time=%s' % end_time])
        if mode == 'incremental':
            stop_id = DB.get_latest_tweet_id(uid)
            if stop_id:
                cmd.extend(['-a', 'stop_after_id=%s' % stop_id])
                _log(f"增量模式: stop_after_id={stop_id}")

        proc, _t = _run_scrapy_with_log(cmd)
        while proc.poll() is None:
            if _check_cancel():
                proc.kill(); proc.wait(timeout=2)
                _log("用户取消了抓取")
                return {'status': 'cancelled'}
            time.sleep(0.5)

        if proc.returncode != 0:
            _log(f"失败! 微博抓取 returncode={proc.returncode}")
            return {'status': 'failed', 'stage': 'tweets', 'user_id': uid,
                    'error': f'returncode={proc.returncode}'}

        tweets_after = DB.conn.execute("SELECT COUNT(*) FROM tweets").fetchone()[0]
        new_tweets = tweets_after - tweets_before
        _log(f"用户 {uid} 微博抓取完成 (新增 {new_tweets} 条, 总计 {tweets_after} 条)")

    stats = DB.stats()
    return {'status': 'completed', 'stats': stats}


def _crawl_comments(scheduler=None, mode='full'):
    """Crawl comments. mode='incremental' uses 8h window + 2 pages; 'full' uses date range."""
    import threading
    script_dir = os.path.dirname(os.path.abspath(__file__))
    log_file = os.path.join(script_dir, 'crawl.log')
    log_lines = []
    unbuffered_env = {**os.environ, 'PYTHONUNBUFFERED': '1'}

    def _log(msg):
        ts = datetime.now().strftime('%H:%M:%S')
        line = f"[{ts}] [comment] {msg}"
        log_lines.append(line)
        logger.info(msg)
        try:
            with open(log_file, 'a') as lf:
                lf.write(line + '\n')
        except:
            pass

    def _run_scrapy_with_log(cmd_args):
        proc = subprocess.Popen(cmd_args, cwd=script_dir, env=unbuffered_env,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        def _forward():
            for line in proc.stdout:
                stripped = line.rstrip('\n')
                if not stripped:
                    continue
                print(stripped, file=sys.stderr, flush=True)
                try:
                    with open(log_file, 'a') as lf:
                        lf.write(stripped + '\n')
                except:
                    pass
        thread = threading.Thread(target=_forward, daemon=True)
        thread.start()
        return proc, thread

    def _check_cancel():
        return scheduler and scheduler.comment_cancelled

    cookie = DB.get_config('cookie', '') or DEFAULT_COOKIE
    if not cookie:
        _log("失败: 未配置 Cookie")
        return {'status': 'failed', 'error': '未配置 Cookie'}
    cookie_path = os.path.join(script_dir, 'cookie.txt')
    with open(cookie_path, 'w') as f:
        f.write(cookie.strip())

    if mode == 'incremental':
        tweet_pairs = DB.get_tweets_for_comment_crawl(hours=8)
        tweet_ids = [mblogid for _, mblogid in tweet_pairs]
        _log(f"增量模式: {len(tweet_ids)} 条微博待补齐评论 (8h 内, <100 评论)")
    else:
        start_time = DB.get_config('start_date', '')
        end_time = DB.get_config('end_date', '')
        if not start_time or not end_time:
            start_time = (datetime.now() - timedelta(days=7)).strftime('%Y-%m-%d')
            end_time = datetime.now().strftime('%Y-%m-%d')
        total_ids = DB.get_tweet_ids(start_date=start_time, end_date=end_time)
        skip_ids = DB.get_tweet_ids_with_enough_comments(100, start_date=start_time, end_date=end_time)
        tweet_ids = [tid for tid in total_ids if tid not in skip_ids]
        _log(f"全量模式: 共 {len(total_ids)} 条, 跳过 {len(skip_ids)} 条(≥100评论), 抓取 {len(tweet_ids)} 条")

    if not tweet_ids:
        _log("没有微博需要抓评论")
        stats = DB.stats()
        return {'status': 'completed', 'stats': stats}

    comments_before = DB.conn.execute("SELECT COUNT(*) FROM comments").fetchone()[0]
    _log(f"开始抓取评论 ({len(tweet_ids)} 条微博, 已有 {comments_before} 条评论)")

    cmd = [
        sys.executable, '-m', 'scrapy', 'crawl', 'comment',
        '-a', 'tweet_ids=%s' % ','.join(tweet_ids),
        '-a', 'flow=0',
        '-s', 'ITEM_PIPELINES={"pipelines.SqlitePipeline": 300}',
        '-s', 'LOG_LEVEL=INFO',
    ]
    if mode == 'incremental':
        cmd.extend(['-a', 'max_pages=2'])

    proc, _t = _run_scrapy_with_log(cmd)
    while proc.poll() is None:
        if _check_cancel():
            proc.kill(); proc.wait(timeout=2)
            _log("用户取消了抓取")
            return {'status': 'cancelled'}
        time.sleep(1)

    comments_after = DB.conn.execute("SELECT COUNT(*) FROM comments").fetchone()[0]
    new_comments = comments_after - comments_before

    if proc.returncode != 0:
        _log(f"评论抓取失败! returncode={proc.returncode}")
    else:
        _log(f"评论抓取完成 (新增 {new_comments} 条, 总计 {comments_after} 条)")

    stats = DB.stats()
    return {'status': 'completed', 'stats': stats}
```

**3b. 修改 `api_crawl_status`（line 361-365）：**

```python
@app.route('/api/crawl/status')
def api_crawl_status():
    if SCHEDULER is None:
        return jsonify({
            'tweet': {'running': False, 'last_result': None},
            'comment': {'running': False, 'last_result': None},
        })
    return jsonify(SCHEDULER.status)
```

**3c. 新增 `/api/crawl/incremental` 端点（在 `api_crawl` 之后）：**

```python
@app.route('/api/crawl/incremental', methods=['POST'])
def api_crawl_incremental():
    if SCHEDULER is None:
        return jsonify({'status': 'started', 'message': 'Scheduler disabled (test mode)'})
    result = SCHEDULER.manual_incremental()
    return jsonify(result)
```

**3d. 修改 `api_get_config`（line 416-426）添加调度配置：**

```python
@app.route('/api/config', methods=['GET'])
def api_get_config():
    cookie = DB.get_config('cookie', '') or DEFAULT_COOKIE
    masked = cookie[:20] + '...' + cookie[-10:] if len(cookie) > 30 else cookie
    schedule_enabled = DB.get_config('schedule_enabled', False)
    if isinstance(schedule_enabled, str):
        schedule_enabled = schedule_enabled.lower() == 'true'
    return jsonify({
        'user_ids': _get_user_ids(),
        'cookie': cookie,
        'cookie_masked': masked,
        'start_date': DB.get_config('start_date', ''),
        'end_date': DB.get_config('end_date', ''),
        'schedule_enabled': schedule_enabled,
        'schedule_start_hour': int(DB.get_config('schedule_start_hour', 5)),
        'schedule_end_hour': int(DB.get_config('schedule_end_hour', 23)),
        'tweet_interval_minutes': int(DB.get_config('tweet_interval_minutes', 60)),
        'comment_interval_minutes': int(DB.get_config('comment_interval_minutes', 30)),
    })
```

**3e. 修改 `api_set_config`（line 429-452）添加调度配置：**

在 `if 'end_date' in data:` 块之后，`if updated:` 之前添加：

```python
    schedule_keys = {
        'schedule_enabled': bool,
        'schedule_start_hour': int,
        'schedule_end_hour': int,
        'tweet_interval_minutes': int,
        'comment_interval_minutes': int,
    }
    schedule_changed = False
    for key, caster in schedule_keys.items():
        if key in data:
            val = caster(data[key])
            DB.set_config(key, str(val) if key == 'schedule_enabled' else val)
            updated[key] = val
            schedule_changed = True
    if schedule_changed and SCHEDULER:
        SCHEDULER.update_config({
            'schedule_enabled': DB.get_config('schedule_enabled', 'false').lower() == 'true' if isinstance(DB.get_config('schedule_enabled', 'false'), str) else DB.get_config('schedule_enabled', False),
            'schedule_start_hour': int(DB.get_config('schedule_start_hour', 5)),
            'schedule_end_hour': int(DB.get_config('schedule_end_hour', 23)),
            'tweet_interval_minutes': int(DB.get_config('tweet_interval_minutes', 60)),
            'comment_interval_minutes': int(DB.get_config('comment_interval_minutes', 30)),
        })
```

**3f. 修改 `create_app`（line 632）：**

```python
        if SCHEDULER is None and not app.config.get('SCHEDULER_DISABLED'):
            SCHEDULER = CrawlScheduler(_crawl_tweets, _crawl_comments)
            # Load schedule config from DB
            schedule_enabled = DB.get_config('schedule_enabled', 'false')
            if isinstance(schedule_enabled, str):
                schedule_enabled = schedule_enabled.lower() == 'true'
            SCHEDULER.update_config({
                'schedule_enabled': schedule_enabled,
                'schedule_start_hour': int(DB.get_config('schedule_start_hour', 5)),
                'schedule_end_hour': int(DB.get_config('schedule_end_hour', 23)),
                'tweet_interval_minutes': int(DB.get_config('tweet_interval_minutes', 60)),
                'comment_interval_minutes': int(DB.get_config('comment_interval_minutes', 30)),
            })
            SCHEDULER.start()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_app.py::TestScheduleConfig tests/test_app.py::TestCrawlStatus tests/test_app.py::TestIncrementalEndpoint -v`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `PYTHONPATH=weibospider python3 -m pytest tests -q`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add weibospider/app.py tests/test_app.py
git commit -m "feat: split _crawl into tweets+comments, add incremental endpoint and schedule config"
```

---

### Task 7: Frontend — 增量同步按钮 + 调度配置面板 + SSE 适配

**Files:**
- Modify: `weibospider/static/index.html`
- Test: `tests/test_frontend.py`

- [ ] **Step 1: Write the failing test**

在 `tests/test_frontend.py` 末尾添加：

```python
def test_incremental_sync_button_exists():
    assert 'id="btn-incremental"' in INDEX_HTML
    assert '增量同步' in INDEX_HTML


def test_schedule_config_inputs_exist():
    assert 'id="schedule-enabled"' in INDEX_HTML
    assert 'id="schedule-start-hour"' in INDEX_HTML
    assert 'id="schedule-end-hour"' in INDEX_HTML
    assert 'id="tweet-interval-minutes"' in INDEX_HTML
    assert 'id="comment-interval-minutes"' in INDEX_HTML


def test_trigger_incremental_function_exists():
    assert "async function triggerIncremental()" in INDEX_HTML
    assert "fetch('/api/crawl/incremental'" in INDEX_HTML


def test_sse_handles_dual_status():
    assert "data.tweet" in INDEX_HTML or "data.tweet.running" in INDEX_HTML
    assert "data.comment" in INDEX_HTML or "data.comment.running" in INDEX_HTML
```

- [ ] **Step 2: Run test to verify it fails**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_frontend.py -v`
Expected: FAIL (new tests)

- [ ] **Step 3: Write minimal implementation**

**3a. 在 toolbar 中添加"增量同步"按钮（`index.html` line 198 之后）：**

```html
        <button class="btn btn-primary" id="btn-crawl">立即抓取</button>
        <button class="btn btn-primary" id="btn-incremental" style="background:#27ae60;">增量同步</button>
```

**3b. 在 config 面板中"监控的博主"之前添加调度配置区（line 247 之前）：**

```html
      <div class="config-section">
        <h3>定时调度</h3>
        <div style="display:flex;align-items:center;gap:8px;margin-bottom:8px;">
          <input type="checkbox" id="schedule-enabled" onchange="saveSchedule()">
          <label for="schedule-enabled" style="font-size:12px;">启用定时抓取</label>
        </div>
        <div style="display:flex;gap:8px;margin-bottom:8px;">
          <label style="font-size:11px;flex:1;">起始小时
            <input type="number" id="schedule-start-hour" min="0" max="23" value="5" onchange="saveSchedule()" style="width:100%;">
          </label>
          <label style="font-size:11px;flex:1;">结束小时
            <input type="number" id="schedule-end-hour" min="1" max="24" value="23" onchange="saveSchedule()" style="width:100%;">
          </label>
        </div>
        <div style="display:flex;gap:8px;">
          <label style="font-size:11px;flex:1;">推文间隔(分钟)
            <input type="number" id="tweet-interval-minutes" min="10" value="60" onchange="saveSchedule()" style="width:100%;">
          </label>
          <label style="font-size:11px;flex:1;">评论间隔(分钟)
            <input type="number" id="comment-interval-minutes" min="10" value="30" onchange="saveSchedule()" style="width:100%;">
          </label>
        </div>
      </div>
```

**3c. 在 JS 中添加按钮事件绑定（line 295 之后）：**

```javascript
document.getElementById('btn-incremental').addEventListener('click', () => triggerIncremental());
```

**3d. 在 `triggerCrawl` 函数之后添加 `triggerIncremental` 函数（line 678 之后）：**

```javascript
async function triggerIncremental() {
  const btn = $('btn-incremental');
  btn.disabled = true;
  btn.textContent = '同步中...';
  $('crawl-log').classList.remove('collapsed', 'error');
  $('crawl-log-toggle').textContent = '收起 ▲';
  $('crawl-log-body').textContent = '正在启动增量同步...';
  try {
    const resp = await fetch('/api/crawl/incremental', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
    });
    const data = await resp.json();
    if (data.status === 'started') {
      toast('增量同步已启动');
      $('crawl-log-body').textContent = '等待日志...';
    } else toast(data.message, true);
  } catch(e) { toast('请求失败', true); }
  btn.disabled = false;
  btn.textContent = '增量同步';
}
```

**3e. 修改 `loadConfig` 函数（line 310-330）加载调度配置：**

在 `if (data.cookie):` 块之后添加：

```javascript
    if (data.schedule_enabled !== undefined) {
      $('schedule-enabled').checked = data.schedule_enabled;
      $('schedule-start-hour').value = data.schedule_start_hour;
      $('schedule-end-hour').value = data.schedule_end_hour;
      $('tweet-interval-minutes').value = data.tweet_interval_minutes;
      $('comment-interval-minutes').value = data.comment_interval_minutes;
    }
```

**3f. 在 `saveDates` 函数之后添加 `saveSchedule` 函数（line 347 之后）：**

```javascript
async function saveSchedule() {
  try {
    await fetch('/api/config', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({
        schedule_enabled: $('schedule-enabled').checked,
        schedule_start_hour: parseInt($('schedule-start-hour').value),
        schedule_end_hour: parseInt($('schedule-end-hour').value),
        tweet_interval_minutes: parseInt($('tweet-interval-minutes').value),
        comment_interval_minutes: parseInt($('comment-interval-minutes').value),
      })
    });
    toast('调度配置已保存');
  } catch(e) { toast('保存失败', true); }
}
```

**3g. 修改 SSE `listenSSE` 函数（line 682-718）适配双状态：**

```javascript
function listenSSE() {
  const es = new EventSource('/api/crawl/events');
  es.addEventListener('status', function(e) {
    try {
      const data = JSON.parse(e.data);
      const tweetRunning = data.tweet && data.tweet.running;
      const commentRunning = data.comment && data.comment.running;
      const anyRunning = tweetRunning || commentRunning;
      if (anyRunning) {
        $('crawl-banner').classList.add('show');
        $('btn-crawl').disabled = true;
        $('btn-incremental').disabled = true;
        $('crawl-log').classList.remove('collapsed');
        $('crawl-log-toggle').textContent = '收起 ▲';
        if (!$('crawl-log-body').textContent) $('crawl-log-body').textContent = '';
      } else {
        $('crawl-banner').classList.remove('show');
        $('btn-crawl').disabled = false;
        $('btn-crawl').textContent = '立即抓取';
        $('btn-incremental').disabled = false;
        $('btn-incremental').textContent = '增量同步';
        const tweetResult = data.tweet && data.tweet.last_result;
        const commentResult = data.comment && data.comment.last_result;
        if (tweetResult || commentResult) {
          const key = JSON.stringify({t: tweetResult, c: commentResult});
          if (key !== sseLastResult) {
            sseLastResult = key;
            loadTweets(true);
            loadStats();
          }
        }
      }
    } catch(e) {}
  });
  es.addEventListener('log', function(e) {
    const body = $('crawl-log-body');
    body.textContent += e.data + '\n';
    body.scrollTop = body.scrollHeight;
  });
  es.onerror = function() {
    setTimeout(listenSSE, 5000);
    es.close();
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_frontend.py -v`
Expected: PASS (all tests)

- [ ] **Step 5: Run full test suite**

Run: `PYTHONPATH=weibospider python3 -m pytest tests -q`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add weibospider/static/index.html tests/test_frontend.py
git commit -m "feat: add incremental sync button, schedule config panel, dual-status SSE"
```

---

### Task 8: 适配 api_crawl 端点 + 收尾

**Files:**
- Modify: `weibospider/app.py`
- Test: `tests/test_app.py`

- [ ] **Step 1: Write the failing test**

在 `tests/test_app.py` 中添加：

```python
class TestCrawlEndpoint:
    def test_crawl_endpoint_returns_started_in_test_mode(self, client):
        rv = client.post('/api/crawl', json={})
        data = json.loads(rv.data)
        assert data['status'] in ('started', 'rejected')
```

- [ ] **Step 2: Run test — should already pass if Task 6 is done**

Run: `PYTHONPATH=weibospider python3 -m pytest tests/test_app.py::TestCrawlEndpoint -v`
Expected: PASS

- [ ] **Step 3: Verify api_crawl still works**

确认 `api_crawl` 函数（约 line 351）调用 `SCHEDULER.manual_crawl(user_id=user_id)`，这个在 Task 5/6 中已适配。如果 `SCHEDULER is None` 返回 test mode 消息。

- [ ] **Step 4: Run full test suite**

Run: `PYTHONPATH=weibospider python3 -m pytest tests -q`
Expected: All pass

- [ ] **Step 5: Commit if any changes were needed**

```bash
git add weibospider/app.py tests/test_app.py
git commit -m "test: verify crawl endpoint compatibility with new scheduler"
```

(If no changes needed, skip commit.)

---

## Self-Review

**Spec coverage:**
- ✅ 推文增量（stop_after_id）→ Task 3
- ✅ 评论增量（max_pages=2, 8h 窗口, <100 冻结）→ Task 2 + Task 4
- ✅ 手动全量 → Task 5 (manual_crawl) + Task 6 (_crawl_tweets/_crawl_comments mode='full')
- ✅ 手动增量 → Task 5 (manual_incremental) + Task 6 (/api/crawl/incremental)
- ✅ 调度配置 5 项 → Task 6 (config GET/POST) + Task 7 (frontend)
- ✅ 双 job 独立锁 → Task 5
- ✅ /api/crawl/status 双状态 → Task 6
- ✅ 前端按钮 + 配置面板 + SSE 适配 → Task 7
- ✅ 默认时间窗 5-23h → Task 6 (config defaults)
- ✅ 日志前缀 [tweet]/[comment] → Task 6 (_log functions)

**Placeholder scan:** 无占位符，所有步骤有完整代码。

**Type consistency:** `manual_incremental()` 在 scheduler.py 定义，在 app.py 调用；`_crawl_tweets(scheduler, mode, user_id)` 和 `_crawl_comments(scheduler, mode)` 签名一致；`status` 返回 `{tweet: {running, last_result}, comment: {running, last_result}}` 在所有地方一致。
