# tests/test_app.py
import io
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
        assert data['tweets'] == []
        assert data['total'] == 0

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
        assert len(data['tweets']) == 3
        assert data['total'] == 3

    def test_ps_endpoint_returns_ps_tweets(self, client):
        import app as app_module
        for _id, content in [('1', '游戏仓6月PS图 本月收盘1696W'),
                             ('2', '普通微博内容')]:
            app_module.DB.insert_tweet({
                '_id': _id, 'mblogid': f'Mb{_id}', 'user_id': '1087770692',
                'content': content, 'created_at': '2026-06-30 15:13:17',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })
        rv = client.get('/api/ps')
        data = json.loads(rv.data)
        assert len(data) == 1
        assert 'PS图' in data[0]['content']

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
        assert 'tweet' in data and 'comment' in data

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

    def test_notes_endpoint_returns_annotated_tweets(self, client):
        import app as app_module
        app_module.DB.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': '有划线评论的微博', 'created_at': '2026-06-30 15:13:17',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        app_module.DB.insert_tweet({
            '_id': '2', 'mblogid': 'Mb2', 'user_id': '1087770692',
            'content': '没有划线评论的微博', 'created_at': '2026-07-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        rv = client.post('/api/tweets/1/annotations', json={
            'start_offset': 0, 'end_offset': 5,
            'selected_text': '有划线评论的微博', 'comment': '这是我的笔记', 'field': 'content',
        })
        assert rv.status_code == 201
        rv = client.get('/api/notes')
        assert rv.status_code == 200
        data = json.loads(rv.data)
        assert len(data) == 1
        assert data[0]['id'] == '1'
        assert data[0]['annotations_list'][0]['comment'] == '这是我的笔记'

    def test_notes_endpoint_empty_when_no_annotations(self, client):
        rv = client.get('/api/notes')
        assert rv.status_code == 200
        assert json.loads(rv.data) == []


class TestOssConfig:
    def test_config_get_has_oss_fields(self, client):
        import app as app_module
        app_module.DB.set_config('oss_access_key_id', 'AKID123')
        app_module.DB.set_config('oss_access_key_secret', 'SECRETVALUE123456789')
        app_module.DB.set_config('oss_bucket', 'mybucket')
        app_module.DB.set_config('oss_endpoint', 'oss-cn-hangzhou.aliyuncs.com')
        rv = client.get('/api/config')
        data = json.loads(rv.data)
        assert data['oss_access_key_id'] == 'AKID123'
        assert data['oss_bucket'] == 'mybucket'
        assert data['oss_endpoint'] == 'oss-cn-hangzhou.aliyuncs.com'
        assert 'SECRETVALUE' not in data['oss_access_key_secret_masked']
        assert data['oss_access_key_secret_masked']

    def test_config_post_oss_fields_then_get(self, client):
        rv = client.post('/api/config', json={
            'oss_access_key_id': 'AKID456',
            'oss_bucket': 'bkt2',
            'oss_endpoint': 'oss-cn-beijing.aliyuncs.com',
            'oss_url_prefix': 'https://img.example.com/',
        })
        assert rv.status_code == 200
        data = json.loads(client.get('/api/config').data)
        assert data['oss_access_key_id'] == 'AKID456'
        assert data['oss_bucket'] == 'bkt2'
        assert data['oss_endpoint'] == 'oss-cn-beijing.aliyuncs.com'
        assert data['oss_url_prefix'] == 'https://img.example.com/'


class TestUpload:
    def _upload(self, client, filename='a.png', ctype='image/png', data=b'x'):
        return client.post('/api/upload',
                           data={'file': (io.BytesIO(data), filename, ctype)},
                           content_type='multipart/form-data')

    def test_upload_non_image_rejected(self, client):
        rv = self._upload(client, filename='a.txt', ctype='text/plain')
        assert rv.status_code == 400
        assert '图片' in json.loads(rv.data)['error']

    def test_upload_oversize_rejected(self, client):
        rv = self._upload(client, data=b'x' * (8 * 1024 * 1024 + 1))
        assert rv.status_code == 400
        assert '8MB' in json.loads(rv.data)['error']

    def test_upload_missing_oss_config_rejected(self, client):
        rv = self._upload(client)
        assert rv.status_code == 400
        assert 'OSS' in json.loads(rv.data)['error']

    def test_upload_success(self, client, monkeypatch):
        import app as app_module
        import types
        app_module.DB.set_config('oss_access_key_id', 'AK')
        app_module.DB.set_config('oss_access_key_secret', 'SK')
        app_module.DB.set_config('oss_bucket', 'bkt')
        app_module.DB.set_config('oss_endpoint', 'oss-cn-hangzhou.aliyuncs.com')
        captured = {}

        class FakeAuth:
            def __init__(self, key, secret):
                pass

        class FakeBucket:
            def __init__(self, auth, endpoint, bucket):
                captured['endpoint'] = endpoint
                captured['bucket'] = bucket

            def put_object(self, key, data, headers=None):
                captured['key'] = key
                captured['data'] = data

        fake = types.ModuleType('oss2')
        fake.Auth = FakeAuth
        fake.Bucket = FakeBucket
        monkeypatch.setitem(sys.modules, 'oss2', fake)

        rv = self._upload(client, data=b'pngdata')
        assert rv.status_code == 200
        data = json.loads(rv.data)
        assert data['url'].startswith('https://bkt.oss-cn-hangzhou.aliyuncs.com/annotations/')
        assert captured['key'].startswith('annotations/')
        assert captured['key'].endswith('.png')
        assert captured['data'] == b'pngdata'


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


class TestLazyComments:
    """A: /api/tweets and /api/ps must NOT embed comments; detail endpoint still does."""

    def _insert_tweet_and_comments(self, client, content='hello'):
        import app as app_module
        app_module.DB.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': content, 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 3, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        for i in range(3):
            app_module.DB.insert_comment({
                '_id': f'c{i}', 'tweet_id': '1', 'content': f'comment {i}',
                'created_at': '2024-01-01 11:00:00', 'like_counts': 0,
                'ip_location': '', 'comment_user': '{}',
                'reply_comment': None, 'crawl_time': 0,
            })

    def test_tweets_list_does_not_embed_comments(self, client):
        self._insert_tweet_and_comments(client)
        rv = client.get('/api/tweets?page=1&per_page=20')
        data = json.loads(rv.data)
        assert len(data['tweets']) == 1
        assert 'comments_list' not in data['tweets'][0]
        assert data['tweets'][0]['comments_count'] == 3

    def test_ps_list_does_not_embed_comments(self, client):
        self._insert_tweet_and_comments(client, content='游戏仓6月PS图 本月收盘1696W')
        rv = client.get('/api/ps')
        data = json.loads(rv.data)
        assert len(data) == 1
        assert 'comments_list' not in data[0]

    def test_detail_endpoint_still_returns_comments(self, client):
        self._insert_tweet_and_comments(client)
        rv = client.get('/api/tweets/1')
        data = json.loads(rv.data)
        assert len(data['comments']) == 3


class TestGzip:
    """B: JSON responses are gzip-compressed when client asks for it."""

    def _insert_many(self, client):
        import app as app_module
        for i in range(20):
            app_module.DB.insert_tweet({
                '_id': str(i), 'mblogid': f'Mb{i}', 'user_id': '1087770692',
                'content': 'x' * 200, 'created_at': f'2024-01-01 10:0{i}:00',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })

    def test_gzip_when_accept_encoding(self, client):
        self._insert_many(client)
        rv = client.get('/api/tweets?page=1&per_page=20',
                        headers={'Accept-Encoding': 'gzip'})
        assert rv.status_code == 200
        assert rv.headers.get('Content-Encoding') == 'gzip'
        import gzip
        data = json.loads(gzip.decompress(rv.data))
        assert data['total'] == 20
        assert len(data['tweets']) == 20

    def test_no_gzip_without_accept_encoding(self, client):
        self._insert_many(client)
        rv = client.get('/api/tweets?page=1&per_page=20')
        assert rv.status_code == 200
        assert rv.headers.get('Content-Encoding') is None
        data = json.loads(rv.data)
        assert data['total'] == 20

    def test_small_response_not_gzipped(self, client):
        rv = client.get('/api/tweets?page=1&per_page=20',
                        headers={'Accept-Encoding': 'gzip'})
        assert rv.status_code == 200
        assert rv.headers.get('Content-Encoding') is None


class TestCrawlStatus:
    def test_status_returns_dual_structure(self, client):
        rv = client.get('/api/crawl/status')
        data = json.loads(rv.data)
        assert 'tweet' in data
        assert 'comment' in data
        assert 'running' in data['tweet']
        assert 'running' in data['comment']

    def test_status_reports_cookie_expired_from_tweet_result(self, client):
        import app as app_module
        fake = type('FakeScheduler', (), {'status': {
            'tweet': {'running': False, 'last_result': {'status': 'failed', 'error': 'Cookie 已过期，请更新 Cookie'}},
            'comment': {'running': False, 'last_result': None},
            'xueqiu': {'running': False, 'last_result': None},
            'xueqiu_comment': {'running': False, 'last_result': None},
        }})()
        old = app_module.SCHEDULER
        app_module.SCHEDULER = fake
        try:
            rv = client.get('/api/crawl/status')
            data = json.loads(rv.data)
            assert data['cookie_expired'] is True
        finally:
            app_module.SCHEDULER = old

    def test_status_cookie_expired_false_when_no_error(self, client):
        import app as app_module
        fake = type('FakeScheduler', (), {'status': {
            'tweet': {'running': False, 'last_result': {'status': 'completed'}},
            'comment': {'running': False, 'last_result': None},
            'xueqiu': {'running': False, 'last_result': None},
            'xueqiu_comment': {'running': False, 'last_result': None},
        }})()
        old = app_module.SCHEDULER
        app_module.SCHEDULER = fake
        import time as _t
        app_module._cookie_probe_cache = {'ts': _t.time(), 'alive': True}
        try:
            rv = client.get('/api/crawl/status')
            data = json.loads(rv.data)
            assert data['cookie_expired'] is False
        finally:
            app_module.SCHEDULER = old
            app_module._cookie_probe_cache = {'ts': 0, 'alive': None}

    def test_status_cookie_expired_when_probe_says_dead(self, client):
        """Even without a crawl error, the liveness probe flags a dead cookie."""
        import app as app_module
        fake = type('FakeScheduler', (), {'status': {
            'tweet': {'running': False, 'last_result': {'status': 'completed'}},
            'comment': {'running': False, 'last_result': None},
            'xueqiu': {'running': False, 'last_result': None},
            'xueqiu_comment': {'running': False, 'last_result': None},
        }})()
        old = app_module.SCHEDULER
        app_module.SCHEDULER = fake
        app_module._cookie_probe_cache = {'ts': 0, 'alive': False}
        try:
            rv = client.get('/api/crawl/status')
            data = json.loads(rv.data)
            assert data['cookie_expired'] is True
        finally:
            app_module.SCHEDULER = old
            app_module._cookie_probe_cache = {'ts': 0, 'alive': None}


class TestIncrementalEndpoint:
    def test_incremental_endpoint_exists(self, client):
        rv = client.post('/api/crawl/incremental')
        assert rv.status_code in (200, 409)
