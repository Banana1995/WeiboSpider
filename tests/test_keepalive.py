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
