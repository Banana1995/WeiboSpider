"""Regression coverage for daily sync, retries, deduplication and timezone repair."""
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import threading
import time
from datetime import datetime, timedelta, timezone
from urllib.parse import parse_qs, urlparse

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'weibospider'))
import app
from db import TweetDB
from scheduler import CrawlScheduler


@pytest.fixture
def db(tmp_path, monkeypatch):
    with TweetDB(str(tmp_path / 'xueqiu.db')) as value:
        value.set_config('xq_cookie', 'test-cookie')
        value.set_config('xq_user_id', '42')
        monkeypatch.setattr(app, 'DB', value)
        monkeypatch.setattr(app, '_make_log_helpers', lambda *args: (lambda *_: None, None))
        monkeypatch.setattr(app.time, 'sleep', lambda *_: None)
        yield value


def status(sid, created='2026-11-03T16:30:00+00:00'):
    return {'id': sid, 'user_id': 42, 'text': '雪球正文',
            'created_at': int(datetime.fromisoformat(created).timestamp() * 1000)}


class Response:
    def __init__(self, data):
        self.data = data

    def __enter__(self):
        return self

    def __exit__(self, *args):
        pass

    def read(self):
        return json.dumps(self.data).encode()


@pytest.mark.parametrize('host_tz', ['UTC', 'Asia/Shanghai', 'America/New_York'])
def test_source_times_are_beijing_in_every_host_timezone(host_tz):
    old_tz = os.environ.get('TZ')
    try:
        os.environ['TZ'] = host_tz
        time.tzset()
        for item in (app._xueqiu_to_item(status(1)),
                     app._xueqiu_comment_to_item(status(2), 'xq1')):
            assert item['created_at'] == '2026-11-04 00:30:00'
            assert item['created_at_timezone'] == 'Asia/Shanghai'
    finally:
        if old_tz is None:
            os.environ.pop('TZ', None)
        else:
            os.environ['TZ'] = old_tz
        time.tzset()


def test_migration_is_explicit_atomic_and_repeat_safe(db):
    db.insert_tweet({'_id': 'xq1', 'content': '旧雪球', 'platform': 'xueqiu',
                     'created_at': '2026-11-03 16:30:00'})
    db.insert_tweet(app._xueqiu_to_item(status(2)))
    db.insert_tweet({'_id': 'weibo', 'content': '微博', 'created_at': '2026-11-03 16:30:00'})
    db.insert_comment({'_id': 'xc1', 'tweet_id': 'xq1', 'content': '旧评论',
                       'platform': 'xueqiu', 'created_at': '2026-11-03 17:00:00'})
    db.insert_comment(app._xueqiu_comment_to_item(status(2), 'xq1'))
    db.set_favorite('xq1', True)
    db.set_ps_mark('xq1', True)
    db.insert_annotation({'id': 'note', 'tweet_id': 'xq1', 'start_offset': 0,
                          'end_offset': 1, 'selected_text': '旧', 'comment': '笔记'})
    assert db.migrate_xueqiu_timestamps('UTC') == {'tweets': 1, 'comments': 1}
    assert db.get_tweet('xq1')['created_at'] == '2026-11-03 16:30:00'
    assert db.migrate_xueqiu_timestamps('UTC', apply=True) == {'tweets': 1, 'comments': 1}
    assert db.migrate_xueqiu_timestamps('UTC', apply=True) == {'tweets': 0, 'comments': 0}
    assert db.get_tweet('xq1')['created_at'] == '2026-11-04 00:30:00'
    assert db.get_tweet('xq2')['created_at'] == '2026-11-04 00:30:00'
    assert db.get_tweet('weibo')['created_at'] == '2026-11-03 16:30:00'
    assert db.conn.execute("SELECT created_at FROM comments WHERE id='xc1'").fetchone()[0] == '2026-11-04 01:00:00'
    assert db.get_favorite_ids(['xq1']) == {'xq1'}
    assert db.get_ps_states(['xq1'])['xq1']['is_ps_manual']
    assert db.get_annotations('xq1')[0]['comment'] == '笔记'
    assert db.conn.execute('PRAGMA foreign_key_check').fetchall() == []


def test_migration_rolls_back_both_tables_on_bad_legacy_date(db):
    db.insert_tweet({'_id': 'xq1', 'content': '旧', 'platform': 'xueqiu',
                     'created_at': '2026-11-03 16:30:00'})
    db.insert_comment({'_id': 'xc1', 'tweet_id': 'xq1', 'content': '旧',
                       'platform': 'xueqiu', 'created_at': 'bad'})
    with pytest.raises(ValueError):
        db.migrate_xueqiu_timestamps('UTC', apply=True)
    assert db.get_tweet('xq1')['created_at'] == '2026-11-03 16:30:00'
    assert db.get_tweet('xq1')['created_at_timezone'] is None


def test_repair_cli_backs_up_before_migrating(db, tmp_path):
    db.insert_tweet({'_id': 'xq1', 'content': '旧', 'platform': 'xueqiu',
                     'created_at': '2026-11-03 16:30:00'})
    backup = tmp_path / 'private' / 'before.db'
    script = Path(app.__file__).with_name('repair_xueqiu_timezone.py')
    result = subprocess.run([sys.executable, str(script), '--db', db.db_path,
                             '--legacy-timezone', 'UTC', '--apply', '--backup', str(backup)],
                            capture_output=True, text=True)
    assert result.returncode == 0, result.stderr
    assert backup.stat().st_mode & 0o777 == 0o600
    with sqlite3.connect(str(backup)) as old:
        assert old.execute("SELECT created_at FROM tweets WHERE id='xq1'").fetchone()[0] == '2026-11-03 16:30:00'
    assert db.get_tweet('xq1')['created_at'] == '2026-11-04 00:30:00'


def test_repeated_full_crawl_updates_instead_of_duplicating(db, monkeypatch):
    monkeypatch.setattr('urllib.request.urlopen', lambda *a, **kw: Response(
        {'statuses': [status(2), status(1)], 'maxPage': 1}))
    assert app._crawl_xueqiu()['stats']['new'] == 2
    db.set_favorite('xq1', True)
    db.set_ps_mark('xq1', True)
    db.batch_delete(['xq1'])
    result = app._crawl_xueqiu()
    assert result['stats'] == {'new': 0, 'updated': 2, 'pages': 1}
    assert db.count_tweets(platform='xueqiu', deleted='all') == 2
    assert db.get_favorite_ids(['xq1']) == {'xq1'}
    assert db.get_tweet('xq1')['deleted'] == 1
    assert db.conn.execute("SELECT COUNT(*) FROM search_index WHERE source_type='tweet'").fetchone()[0] == 2


def test_recrawl_repairs_legacy_time_without_shifting_again(db):
    item = app._xueqiu_to_item(status(1))
    db.insert_tweet({**item, 'created_at': '2026-11-03 16:30:00', 'created_at_timezone': None})
    assert db.insert_tweet(item) is False
    assert db.migrate_xueqiu_timestamps('UTC', apply=True)['tweets'] == 0
    assert db.get_tweet('xq1')['created_at'] == '2026-11-04 00:30:00'


def test_incremental_does_not_stop_at_pinned_post_or_lose_a_failed_page(db, monkeypatch):
    db.insert_tweet(app._xueqiu_to_item(status(100)))
    calls = []
    fail_page_two = [True]

    def fetch(req, **kw):
        page = int(parse_qs(urlparse(req.full_url).query)['page'][0])
        calls.append(page)
        if page == 2 and fail_page_two[0]:
            raise OSError('offline')
        ids = [1, 104, 103] if page == 1 else [102, 101, 100]
        return Response({'statuses': [status(i) for i in ids], 'maxPage': 2})

    monkeypatch.setattr('urllib.request.urlopen', fetch)
    assert app._crawl_xueqiu(mode='incremental')['status'] == 'failed'
    assert db.get_config('xq_timeline_checkpoint_42') == 'xq100'
    fail_page_two[0] = False
    calls.clear()
    result = app._crawl_xueqiu(mode='incremental')
    assert result['stats'] == {'new': 2, 'updated': 2, 'pages': 2}
    assert calls == [1, 2]
    assert db.get_tweet('xq101') is not None
    assert db.get_config('xq_timeline_checkpoint_42') == 'xq104'


def test_incremental_comments_keep_history_and_retry_older_new_posts(db, monkeypatch):
    db.insert_tweet(app._xueqiu_to_item(status(1, '2020-01-01T00:00:00+00:00')))
    db.insert_comment({'_id': 'xc99', 'tweet_id': 'xq1', 'content': '历史评论', 'platform': 'xueqiu'})
    monkeypatch.setattr('urllib.request.urlopen', lambda *a, **kw: Response({'error_code': '400016'}))
    assert app._crawl_xueqiu_comments(mode='incremental')['status'] == 'failed'
    assert db.get_comment_ids('xq1') == {'xc99'}
    assert db.get_xueqiu_tweets_for_comment_crawl(False, True) == [('xq1', '1')]
    monkeypatch.setattr('urllib.request.urlopen', lambda *a, **kw: Response(
        {'comments': [status(1)], 'next_max_id': -1}))
    assert app._crawl_xueqiu_comments(mode='incremental')['stats']['new'] == 1
    assert db.get_comment_ids('xq1') == {'xc1', 'xc99'}
    assert db.get_xueqiu_tweets_for_comment_crawl(False, True) == []


def test_incremental_comment_window_is_beijing_48h_and_all_post_types(db):
    now = datetime.now(timezone.utc)
    for sid, hours in [(1, 47), (2, 49)]:
        db.insert_tweet(app._xueqiu_to_item(status(sid, (now - timedelta(hours=hours)).isoformat())))
        db.batch_insert_comments([], xueqiu_tweet_id=f'xq{sid}')
    assert db.get_xueqiu_tweets_for_comment_crawl(False, True) == [('xq1', '1')]
    db.batch_delete(['xq1'])
    assert db.get_xueqiu_tweets_for_comment_crawl(False, True) == []


def test_weibo_comment_targets_never_include_xueqiu_ids(db):
    now = datetime.now(timezone.utc)
    db.insert_tweet(app._xueqiu_to_item(status(1, now.isoformat())))
    db.insert_tweet({'_id': 'weibo', 'mblogid': 'WeiboID', 'content': '微博',
                     'created_at': now.astimezone(timezone(timedelta(hours=8))).strftime('%Y-%m-%d %H:%M:%S')})
    assert db.get_tweets_for_comment_crawl() == [('weibo', 'WeiboID')]
    assert db.get_tweet_ids() == ['WeiboID']
    assert db.get_tweet_ids('2000-01-01', '2100-01-01') == ['WeiboID']
    for tid in ('xq1', 'weibo'):
        db.insert_comment({'_id': tid + '-comment', 'tweet_id': tid, 'content': '评论'})
    assert db.get_tweet_ids_with_enough_comments(1) == ['WeiboID']
    assert db.get_tweet_ids_with_enough_comments(1, '2000-01-01', '2100-01-01') == ['WeiboID']


def test_comment_network_failure_after_first_page_keeps_all_old_comments(db, monkeypatch):
    db.insert_tweet(app._xueqiu_to_item(status(1)))
    db.insert_comment({'_id': 'xc99', 'tweet_id': 'xq1', 'content': '旧', 'platform': 'xueqiu'})
    calls = []

    def fetch(req, **kw):
        calls.append(req.full_url)
        if len(calls) == 2:
            raise OSError('offline')
        return Response({'comments': [status(1)], 'next_max_id': 123})

    monkeypatch.setattr('urllib.request.urlopen', fetch)
    assert app._crawl_xueqiu_comments(mode='incremental')['status'] == 'failed'
    assert db.get_comment_ids('xq1') == {'xc99'}


def test_daily_job_and_manual_incremental_run_posts_before_comments(monkeypatch):
    events = []
    def posts(sch, **kw):
        events.append(('posts', kw['mode']))
        return {'status': 'completed'}
    def comments(sch, **kw):
        assert sch.status['xueqiu']['running'] is True
        events.append(('comments', kw['mode']))
        return {'status': 'completed'}
    sch = CrawlScheduler(None, None, posts, comments)
    sch.update_config({'schedule_enabled': True})
    trigger = sch._scheduler.get_job('xueqiu_crawl').trigger
    now = datetime(2026, 11, 3, 12, 0, tzinfo=timezone.utc)
    fire = trigger.get_next_fire_time(None, now)
    assert fire.astimezone(timezone.utc) == datetime(2026, 11, 3, 13, 0, tzinfo=timezone.utc)
    assert trigger.get_next_fire_time(fire, fire) - fire == timedelta(days=1)
    monkeypatch.setattr(sch, '_within_window', lambda: True)
    sch._scheduled_xueqiu_crawl()
    assert events == [('posts', 'incremental'), ('comments', 'incremental')]
    sch.update_config({'schedule_enabled': False})
    assert sch._scheduler.get_jobs() == []


@pytest.mark.parametrize('outcome', ['failed', 'cancelled', 'cancel_after_posts'])
def test_daily_comments_do_not_start_after_failure_or_cancel(outcome):
    events = []
    def posts(sch, **kw):
        if outcome == 'cancel_after_posts':
            sch.cancel()
            return {'status': 'completed'}
        return {'status': outcome}
    sch = CrawlScheduler(None, None, posts, lambda *a, **kw: events.append('comments'))
    sch._execute_xueqiu_job(mode='incremental', with_comments=True)
    assert events == []
    assert sch.status['xueqiu']['running'] is False


def test_daily_pipeline_excludes_concurrent_manual_comment_job():
    entered = threading.Event()
    release = threading.Event()
    comments = []
    def posts(sch, **kw):
        entered.set()
        assert release.wait(5)
        return {'status': 'completed'}
    def crawl_comments(sch, **kw):
        comments.append(kw['mode'])
        return {'status': 'completed'}
    sch = CrawlScheduler(None, None, posts, crawl_comments)
    worker = threading.Thread(target=sch._execute_xueqiu_job,
                              kwargs={'mode': 'incremental', 'with_comments': True})
    worker.start()
    try:
        assert entered.wait(5)
        assert sch._execute_xueqiu_comment_job()['status'] == 'skipped'
        assert sch.manual_xueqiu_comments()['status'] == 'rejected'
    finally:
        release.set()
        worker.join(5)
    assert comments == ['incremental']
