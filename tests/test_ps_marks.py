"""Manual PS classification supplements keywords and survives archive updates."""
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'weibospider'))

from db import TweetDB


def post(db, tweet_id, content, date='2026-11-01 12:00:00', **extra):
    item = {'_id': tweet_id, 'user_id': 'author', 'content': content,
            'created_at': date, **extra}
    db.insert_tweet(item)
    return item


def ids(rows):
    return [row['id'] for row in rows]


@pytest.fixture
def ps_db(tmp_path):
    with TweetDB(str(tmp_path / 'ps.db')) as db:
        post(db, 'manual', '本月持仓记录', '2026-11-01 12:00:00')
        post(db, 'auto', '十月PS图', '2026-10-31 12:00:00')
        post(db, 'lowercase', '九月ps图', '2026-09-30 12:00:00')
        post(db, 'ordinary', '普通微博')
        yield db


def test_ps_feed_unions_manual_and_keyword_matches_without_duplicates(ps_db):
    db = ps_db
    assert ids(db.get_ps_tweets()) == ['auto', 'lowercase']
    for tweet_id in ('manual', 'auto', 'lowercase'):
        assert db.set_ps_mark(tweet_id, True) == {'is_ps_manual': True, 'is_ps': True}
    assert ids(db.get_ps_tweets()) == ['manual', 'auto', 'lowercase']
    assert db.get_tweet('manual')['content'] == '本月持仓记录'
    for tweet_id in ('auto', 'lowercase'):
        assert db.set_ps_mark(tweet_id, False) == {'is_ps_manual': False, 'is_ps': True}
    assert db.set_ps_mark('manual', False) == {'is_ps_manual': False, 'is_ps': False}
    assert ids(db.get_ps_tweets()) == ['auto', 'lowercase']


def test_existing_database_upgrade_and_recrawls_preserve_manual_mark(tmp_path):
    path = str(tmp_path / 'existing.db')
    with TweetDB(path) as db:
        item = post(db, 'kept', '持仓原文')
        db.set_favorite('kept', True)
        db.insert_annotation({
            'id': 'note', 'tweet_id': 'kept', 'start_offset': 0, 'end_offset': 2,
            'selected_text': '持仓', 'comment': '已有笔记', 'field': 'content',
        })
        # Model a database from before manual PS marking was introduced.
        db.conn.execute('DROP TABLE ps_marks')
        db.conn.commit()
    with TweetDB(path) as db:
        assert db.get_ps_states(['kept']) == {
            'kept': {'is_ps_manual': False, 'is_ps': False}}
        assert db.set_ps_mark('missing', True) is None
        db.set_ps_mark('kept', True)
        first = tuple(db.conn.execute('SELECT * FROM ps_marks').fetchone())
        db.set_ps_mark('kept', True)
        assert [tuple(r) for r in db.conn.execute('SELECT * FROM ps_marks')] == [first]
        db.insert_tweet({**item, 'content': '单条抓取更新'})
        db.batch_insert_tweets([{**item, 'content': '批量抓取更新'}])
    with TweetDB(path) as db:
        assert ids(db.get_ps_tweets()) == ['kept']
        assert db.get_ps_states(['kept'])['kept']['is_ps_manual'] is True
        assert db.get_tweet('kept')['content'] == '批量抓取更新'
        assert db.get_favorite_ids(['kept']) == {'kept'}
        assert db.get_annotations('kept')[0]['comment'] == '已有笔记'
        db.batch_delete(['kept'])
        assert db.get_ps_tweets() == []
        db.restore_tweets(['kept'])
        assert ids(db.get_ps_tweets()) == ['kept']
        db.set_ps_mark('kept', False)
        db.set_ps_mark('kept', False)
        assert db.get_ps_tweets() == []


def test_xueqiu_comment_targets_use_the_same_ps_classification(ps_db):
    db = ps_db
    post(db, 'xq1', '持仓总结', '2026-11-01 12:00:00', platform='xueqiu')
    post(db, 'xq2', 'PS图', '2026-10-31 12:00:00', platform='xueqiu')
    post(db, 'xq3', '普通内容', platform='xueqiu')
    db.set_ps_mark('xq1', True)
    db.set_ps_mark('manual', True)
    assert db.get_xueqiu_tweets_for_comment_crawl() == [('xq1', '1'), ('xq2', '2')]
    db.batch_delete(['xq1'])
    assert db.get_xueqiu_tweets_for_comment_crawl() == [('xq2', '2')]
    assert set(db.get_xueqiu_tweets_for_comment_crawl(ps_only=False)) == {
        ('xq2', '2'), ('xq3', '3')}


@pytest.fixture
def ps_client(ps_db, monkeypatch):
    monkeypatch.setenv('SCRAPY_SETTINGS_MODULE', 'settings')
    import app as app_module
    monkeypatch.setattr(app_module, 'DB', ps_db)
    monkeypatch.setitem(app_module.app.config, 'TESTING', True)
    monkeypatch.setitem(app_module.app.config, 'SCHEDULER_DISABLED', True)
    return app_module.app.test_client()


def test_mark_api_and_all_card_views_agree(ps_client, ps_db):
    client = ps_client
    ps_db.set_favorite('manual', True)
    for state in (True, True, False, False):
        response = client.put('/api/tweets/manual/ps', json={'is_ps_manual': state})
        assert response.status_code == 200
        assert response.get_json() == {
            'id': 'manual', 'is_ps_manual': state, 'is_ps': state}
        views = [
            client.get('/api/tweets/manual').get_json()['tweet'],
            next(t for t in client.get('/api/tweets').get_json()['tweets']
                 if t['id'] == 'manual'),
            client.get('/api/search?q=持仓').get_json()['results'][0],
            client.get('/api/notes').get_json()[0],
        ]
        for tweet in views:
            assert tweet['is_ps_manual'] is state
            assert tweet['is_ps'] is state
            assert tweet['content'] == '本月持仓记录'
        feed = client.get('/api/ps').get_json()
        assert ('manual' in ids(feed)) is state
        assert all(t['is_ps'] is True for t in feed)
        assert next(t for t in feed if t['id'] == 'auto')['is_ps_manual'] is False
    ps_db.set_ps_mark('auto', True)
    response = client.put('/api/tweets/auto/ps', json={'is_ps_manual': False})
    assert response.get_json() == {'id': 'auto', 'is_ps_manual': False, 'is_ps': True}
    assert ids(client.get('/api/ps').get_json()) == ['auto', 'lowercase']


@pytest.mark.parametrize('body', [None, {}, [], {'is_ps_manual': None},
                                 {'is_ps_manual': 'true'}, {'is_ps_manual': 1}])
def test_invalid_mark_request_preserves_existing_state(ps_client, ps_db, body):
    ps_db.set_ps_mark('manual', True)
    response = ps_client.put('/api/tweets/manual/ps', json=body)
    assert response.status_code == 400
    assert ps_db.get_ps_states(['manual'])['manual']['is_ps_manual'] is True


def test_mark_api_rejects_missing_post_and_malformed_json(ps_client):
    assert ps_client.put('/api/tweets/missing/ps', json={
        'is_ps_manual': True}).status_code == 404
    assert ps_client.put('/api/tweets/manual/ps', data='{',
                         content_type='application/json').status_code == 400
