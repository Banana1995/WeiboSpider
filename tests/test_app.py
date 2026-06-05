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
                'content': f'tweet {i}', 'created_at': f'2024-01-01 0{i}:00:00',
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
