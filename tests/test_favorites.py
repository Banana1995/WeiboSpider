"""Favorites persist independently of crawling and share a filtered notes feed."""
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'weibospider'))

from db import TweetDB


@pytest.fixture
def favorites_db(tmp_path):
    with TweetDB(str(tmp_path / 'favorites.db')) as db:
        yield db


def post(db, tweet_id, content, date='2026-10-31 12:00:00', **extra):
    item = {'_id': tweet_id, 'user_id': 'author', 'content': content,
            'created_at': date, 'mblogid': tweet_id, **extra}
    db.insert_tweet(item)
    return item


def note(db, tweet_id, ann_id, comment, selected_text='摘录'):
    return db.insert_annotation({
        'id': ann_id, 'tweet_id': tweet_id, 'start_offset': 0,
        'end_offset': len(selected_text), 'selected_text': selected_text,
        'comment': comment, 'field': 'content',
    })


def ids(rows):
    return [row['id'] for row in rows]


@pytest.fixture
def saved_posts(favorites_db):
    db = favorites_db
    post(db, 'favorite', '黄金研究\n第二行 50%_折扣', '2026-10-31 00:00:00',
         retweet_content='原始转发 Copper')
    post(db, 'note', '铜价观察', '2026-11-01 12:00:00')
    post(db, 'both', 'PS图 投资记录', '2026-10-31 23:59:59', platform='xueqiu')
    post(db, 'ordinary', '黄金 估值', '2026-11-02 12:00:00')
    post(db, 'trash', '黄金已删除')
    for tweet_id in ('favorite', 'both', 'trash'):
        db.set_favorite(tweet_id, True)
    note(db, 'note', 'ann-note', '估值需要耐心', '铜价')
    note(db, 'note', 'ann-note-2', '风险折价')
    note(db, 'both', 'ann-both', '现金流')
    note(db, 'trash', 'ann-trash', '估值')
    db.batch_delete(['trash'])
    return db


def test_saved_feed_unions_and_deduplicates_posts(saved_posts):
    assert ids(saved_posts.get_saved_tweets()) == ['note', 'both', 'favorite']
    assert ids(saved_posts.get_saved_tweets(kind='favorites')) == ['both', 'favorite']
    assert ids(saved_posts.get_saved_tweets(kind='notes')) == ['note', 'both']
    assert ids(saved_posts.get_annotated_tweets()) == ['note', 'both']
    assert [r['is_favorited'] for r in saved_posts.get_saved_tweets()] == [False, True, True]


@pytest.mark.parametrize('query, expected', [
    ('黄金', ['favorite']),
    ('第二行', ['favorite']),
    ('cOpPeR', ['favorite']),
    ('估值', ['note']),
    ('铜价', ['note']),
    ('摘录', ['note', 'both']),
    ('  现金流  ', ['both']),
    ('%_', ['favorite']),
    ("%' OR 1=1--", []),
])
def test_keyword_matches_saved_bodies_retweets_and_notes(saved_posts, query, expected):
    assert ids(saved_posts.get_saved_tweets(q=query)) == expected


def test_filters_combine_and_include_entire_end_date(saved_posts):
    assert ids(saved_posts.get_saved_tweets(
        start_date='2026-10-31', end_date='2026-10-31')) == ['both', 'favorite']
    assert ids(saved_posts.get_saved_tweets(
        kind='favorites', platform='xueqiu', q='现金流',
        start_date='2026-10-31', end_date='2026-10-31')) == ['both']
    assert saved_posts.get_saved_tweets(kind='notes', platform='weibo', q='现金流') == []


def test_unfavorite_keeps_notes_and_deleting_last_note_keeps_favorite(saved_posts):
    db = saved_posts
    assert db.set_favorite('both', False) is False
    assert 'both' in ids(db.get_saved_tweets())
    assert len(db.get_annotations('both')) == 1
    db.set_favorite('both', True)
    db.delete_annotation('ann-both')
    assert 'both' in ids(db.get_saved_tweets())
    assert 'both' not in ids(db.get_saved_tweets(kind='notes'))
    db.set_favorite('both', False)
    assert 'both' not in ids(db.get_saved_tweets())


def test_favorites_are_idempotent_and_survive_recrawl_and_reopen(tmp_path):
    path = str(tmp_path / 'existing.db')
    with TweetDB(path) as db:
        item = post(db, 'kept', '原文\n第二行')
        note(db, 'kept', 'ann', '旧笔记')
        # Model an existing database from before the favorites feature.
        db.conn.execute('DROP TABLE favorites')
        db.conn.commit()
    with TweetDB(path) as db:
        assert db.get_favorite_ids(['kept']) == set()
        assert db.get_annotations('kept')[0]['comment'] == '旧笔记'
        assert db.set_favorite('missing', True) is None
        assert db.set_favorite('kept', True) is True
        first = tuple(db.conn.execute('SELECT * FROM favorites').fetchone())
        db.set_favorite('kept', True)
        assert [tuple(r) for r in db.conn.execute('SELECT * FROM favorites')] == [first]
        db.insert_tweet({**item, 'content': '单条重新抓取'})
        db.batch_insert_tweets([{**item, 'content': '批量重新抓取'}])
        assert db.get_favorite_ids(['kept']) == {'kept'}
    with TweetDB(path) as db:
        assert db.get_favorite_ids([]) == set()
        assert db.get_favorite_ids(['kept']) == {'kept'}
        assert db.get_tweet('kept')['content'] == '批量重新抓取'
        db.batch_delete(['kept'])
        assert db.get_saved_tweets() == []
        db.restore_tweets(['kept'])
        assert ids(db.get_saved_tweets(kind='favorites')) == ['kept']
        db.set_favorite('kept', False)
        db.set_favorite('kept', False)
        assert ids(db.get_saved_tweets()) == ['kept']


@pytest.fixture
def favorite_client(saved_posts, monkeypatch):
    monkeypatch.setenv('SCRAPY_SETTINGS_MODULE', 'settings')
    import app as app_module
    monkeypatch.setattr(app_module, 'DB', saved_posts)
    monkeypatch.setitem(app_module.app.config, 'TESTING', True)
    monkeypatch.setitem(app_module.app.config, 'SCHEDULER_DISABLED', True)
    return app_module.app.test_client()


def test_favorite_api_state_is_consistent_across_views(favorite_client):
    client = favorite_client
    for state in (True, True, False, False):
        response = client.put('/api/tweets/ordinary/favorite', json={'is_favorited': state})
        assert response.status_code == 200
        assert response.get_json() == {'id': 'ordinary', 'is_favorited': state}
        assert client.get('/api/tweets/ordinary').get_json()['tweet']['is_favorited'] is state
        rows = client.get('/api/tweets').get_json()['tweets']
        assert next(r for r in rows if r['id'] == 'ordinary')['is_favorited'] is state
        results = client.get('/api/search?q=黄金').get_json()['results']
        assert next(r for r in results if r['id'] == 'ordinary')['is_favorited'] is state
        assert ('ordinary' in ids(client.get('/api/notes').get_json())) is state
    assert client.get('/api/ps').get_json()[0]['is_favorited'] is True


def test_notes_api_combines_filters_and_returns_annotations(favorite_client):
    response = favorite_client.get('/api/notes', query_string={
        'kind': 'favorites', 'platform': 'xueqiu', 'q': '现金流',
        'start_date': '2026-10-31', 'end_date': '2026-10-31',
    })
    assert response.status_code == 200
    rows = response.get_json()
    assert ids(rows) == ['both']
    assert rows[0]['annotations_list'][0]['comment'] == '现金流'
    assert rows[0]['is_favorited'] is True
    assert rows[0]['comment_counts']['total'] == 0
    assert 'comments_list' not in rows[0]


@pytest.mark.parametrize('body', [{}, [], {'is_favorited': None},
                                 {'is_favorited': 'true'}, {'is_favorited': 1}])
def test_favorite_api_rejects_non_boolean_without_changing_state(favorite_client, body):
    response = favorite_client.put('/api/tweets/ordinary/favorite', json=body)
    assert response.status_code == 400
    assert favorite_client.get('/api/tweets/ordinary').get_json()['tweet']['is_favorited'] is False


def test_favorite_api_missing_post_and_invalid_json(favorite_client):
    assert favorite_client.put('/api/tweets/missing/favorite', json={
        'is_favorited': True}).status_code == 404
    assert favorite_client.put('/api/tweets/ordinary/favorite',
                               data='{', content_type='application/json').status_code == 400


@pytest.mark.parametrize('filters', [
    {'kind': 'unknown'}, {'platform': 'unknown'}, {'q': '字' * 201},
    {'start_date': '2026-02-30'}, {'end_date': '2026-1-1'},
    {'start_date': '2026-11-01', 'end_date': '2026-10-31'},
])
def test_notes_api_rejects_invalid_filters(favorite_client, filters):
    response = favorite_client.get('/api/notes', query_string=filters)
    assert response.status_code == 400
    assert response.get_json()['error']
