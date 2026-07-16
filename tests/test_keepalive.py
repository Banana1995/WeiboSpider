# tests/test_keepalive.py
"""Tests for cookie keepalive: refresh session cookie by pinging weibo.com."""
import os
import sys
import tempfile
import pytest
from unittest.mock import patch, MagicMock
from http.cookiejar import CookieJar

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))


@pytest.fixture
def db():
    fd, path = tempfile.mkstemp(suffix='.db')
    os.close(fd)
    from db import TweetDB
    d = TweetDB(path)
    d.set_config('cookie', 'SUB=old_sub; ALF=old_alf; SCF=old_scf')
    yield d
    d.close()
    os.unlink(path)
    try:
        os.unlink(path + '.plock')
    except OSError:
        pass


class TestParseSetCookie:
    def test_parse_single_set_cookie(self):
        from keepalive import _merge_set_cookie_into_cookie_string
        old = 'SUB=old; ALF=old; SCF=old'
        set_cookie_headers = ['SUB=new_sub; Path=/; Domain=.weibo.com; HttpOnly']
        result = _merge_set_cookie_into_cookie_string(old, set_cookie_headers)
        assert 'SUB=new_sub' in result
        assert 'ALF=old' in result
        assert 'SCF=old' in result

    def test_parse_multiple_set_cookie_headers(self):
        from keepalive import _merge_set_cookie_into_cookie_string
        old = 'SUB=old; ALF=old'
        set_cookie_headers = [
            'SUB=new_sub; Path=/; HttpOnly',
            'ALF=new_alf; Path=/; HttpOnly',
        ]
        result = _merge_set_cookie_into_cookie_string(old, set_cookie_headers)
        assert 'SUB=new_sub' in result
        assert 'ALF=new_alf' in result

    def test_set_cookie_with_expires_in_future_kept(self):
        from keepalive import _merge_set_cookie_into_cookie_string
        old = 'SUB=old; ALF=old'
        set_cookie_headers = ['SUB=new; Expires=Wed, 31 Dec 2099 23:59:59 GMT']
        result = _merge_set_cookie_into_cookie_string(old, set_cookie_headers)
        assert 'SUB=new' in result

    def test_no_set_cookie_headers_returns_original(self):
        from keepalive import _merge_set_cookie_into_cookie_string
        old = 'SUB=old; ALF=old'
        result = _merge_set_cookie_into_cookie_string(old, [])
        assert result == old


class TestRefreshCookie:
    def test_refresh_updates_cookie_in_db_when_set_cookie_returned(self, db):
        from keepalive import refresh_cookie
        fake_response = MagicMock()
        fake_response.status = 200
        fake_headers = MagicMock()
        fake_headers.get_all = lambda name=None: {
            'Set-Cookie': [
                'SUB=fresh_sub; Path=/; Domain=.weibo.com; HttpOnly',
                'ALF=fresh_alf; Path=/; Domain=.weibo.com; HttpOnly',
            ],
        }.get(name, [])
        fake_response.headers = fake_headers
        with patch('keepalive._ping_weibo', return_value=fake_response):
            result = refresh_cookie(db)
        assert result['refreshed'] is True
        new_cookie = db.get_config('cookie')
        assert 'SUB=fresh_sub' in new_cookie
        assert 'ALF=fresh_alf' in new_cookie
        assert 'SCF=old_scf' in new_cookie

    def test_refresh_no_set_cookie_returns_not_refreshed(self, db):
        from keepalive import refresh_cookie
        fake_response = MagicMock()
        fake_response.status = 200
        fake_headers = MagicMock()
        fake_headers.get_all = lambda name=None: [] if name == 'Set-Cookie' else []
        fake_response.headers = fake_headers
        with patch('keepalive._ping_weibo', return_value=fake_response):
            result = refresh_cookie(db)
        assert result['refreshed'] is False
        assert db.get_config('cookie') == 'SUB=old_sub; ALF=old_alf; SCF=old_scf'

    def test_refresh_request_failure_returns_error(self, db):
        from keepalive import refresh_cookie
        with patch('keepalive._ping_weibo', side_effect=Exception('network error')):
            result = refresh_cookie(db)
        assert 'error' in result
        assert db.get_config('cookie') == 'SUB=old_sub; ALF=old_alf; SCF=old_scf'

    def test_refresh_no_cookie_configured_returns_error(self, db):
        from keepalive import refresh_cookie
        db.set_config('cookie', '')
        with patch('keepalive._ping_weibo') as mock_ping:
            result = refresh_cookie(db)
        assert 'error' in result
        mock_ping.assert_not_called()


class TestAlfExpiry:
    def test_get_alf_expiry_plain_timestamp(self):
        from keepalive import get_alf_expiry
        assert get_alf_expiry('SUB=abc; ALF=1786764565; SCF=def') == 1786764565

    def test_get_alf_expiry_prefixed_timestamp(self):
        from keepalive import get_alf_expiry
        assert get_alf_expiry('SUB=abc; ALF=02_1782965565; SCF=def') == 1782965565

    def test_get_alf_expiry_missing_returns_none(self):
        from keepalive import get_alf_expiry
        assert get_alf_expiry('SUB=abc; SCF=def') is None

    def test_get_alf_expiry_empty_returns_none(self):
        from keepalive import get_alf_expiry
        assert get_alf_expiry('') is None

    def test_should_keepalive_now_true_when_within_3_days(self):
        from keepalive import should_keepalive_now
        import time
        now = time.time()
        cookie = f'SUB=x; ALF={int(now + 2 * 86400)}'
        assert should_keepalive_now(cookie, now=now) is True

    def test_should_keepalive_now_false_when_far_from_expiry(self):
        from keepalive import should_keepalive_now
        import time
        now = time.time()
        cookie = f'SUB=x; ALF={int(now + 30 * 86400)}'
        assert should_keepalive_now(cookie, now=now) is False

    def test_should_keepalive_now_true_when_already_expired(self):
        from keepalive import should_keepalive_now
        import time
        now = time.time()
        cookie = f'SUB=x; ALF={int(now - 100)}'
        assert should_keepalive_now(cookie, now=now) is True

    def test_should_keepalive_now_true_when_alf_missing(self):
        from keepalive import should_keepalive_now
        assert should_keepalive_now('SUB=abc; SCF=def') is True


class TestOperationLogs:
    def test_insert_and_retrieve_log(self, db):
        db.insert_log('crawl', 'manual_incremental', detail='test', status='success', user='web')
        logs = db.get_logs(page=1, per_page=10)
        assert logs['total'] == 1
        assert logs['logs'][0]['category'] == 'crawl'
        assert logs['logs'][0]['action'] == 'manual_incremental'
        assert logs['logs'][0]['status'] == 'success'

    def test_logs_ordered_newest_first(self, db):
        db.insert_log('crawl', 'first')
        db.insert_log('crawl', 'second')
        logs = db.get_logs(page=1, per_page=10)
        assert logs['logs'][0]['action'] == 'second'
        assert logs['logs'][1]['action'] == 'first'

    def test_logs_filter_by_category(self, db):
        db.insert_log('crawl', 'tweet')
        db.insert_log('keepalive', 'ping')
        db.insert_log('annotation', 'create')
        logs = db.get_logs(page=1, per_page=10, category='keepalive')
        assert logs['total'] == 1
        assert logs['logs'][0]['action'] == 'ping'

    def test_logs_pagination(self, db):
        for i in range(5):
            db.insert_log('crawl', f'action_{i}')
        page1 = db.get_logs(page=1, per_page=2)
        page2 = db.get_logs(page=2, per_page=2)
        assert page1['total'] == 5
        assert len(page1['logs']) == 2
        assert len(page2['logs']) == 2
        assert page1['logs'][0]['action'] == 'action_4'
        assert page2['logs'][0]['action'] == 'action_2'
