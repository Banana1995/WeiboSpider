# tests/test_keepalive.py
"""Tests for read-only cookie status checks and operation logs."""
import os
import sys
import tempfile
import pytest
from unittest.mock import patch, MagicMock

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


class TestCheckCookieAlive:
    """Liveness probe: hit mymblog API and judge session by real response."""

    def _mock_urlopen(self, body=b'', ctype='application/json'):
        fake = MagicMock()
        headers = MagicMock()
        headers.get = lambda name: ctype if name == 'Content-Type' else None
        fake.headers = headers
        fake.status = 200
        fake.read.return_value = body
        return fake

    def test_alive_when_ok_1(self):
        from keepalive import check_cookie_alive
        import json
        fake = self._mock_urlopen(json.dumps({'ok': 1, 'data': {'list': []}}).encode())
        with patch('keepalive._probe_mymblog', return_value=fake):
            assert check_cookie_alive('SUB=abc') is True

    def test_dead_when_ok_negative(self):
        from keepalive import check_cookie_alive
        import json
        fake = self._mock_urlopen(json.dumps({'ok': -100, 'msg': 'not logged in'}).encode())
        with patch('keepalive._probe_mymblog', return_value=fake):
            assert check_cookie_alive('SUB=abc') is False

    def test_dead_when_html_redirect(self):
        from keepalive import check_cookie_alive
        fake = self._mock_urlopen(
            '<html><title>安全验证</title><meta http-equiv="refresh" '
            "content=\"0; url='...retcode=6102...'\"></html>".encode('utf-8'),
            ctype='text/html')
        with patch('keepalive._probe_mymblog', return_value=fake):
            assert check_cookie_alive('SUB=abc') is False

    def test_unknown_on_network_error(self):
        from keepalive import check_cookie_alive
        with patch('keepalive._probe_mymblog', side_effect=Exception('net down')):
            assert check_cookie_alive('SUB=abc') is None

    def test_dead_when_no_cookie(self):
        from keepalive import check_cookie_alive
        with patch('keepalive._probe_mymblog') as mock_probe:
            assert check_cookie_alive('') is False
        mock_probe.assert_not_called()


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

    def test_cleanup_old_logs_deletes_old(self, db):
        from datetime import datetime, timedelta
        # Insert an old log (20 days ago) by manipulating timestamp via direct SQL
        old_ts = (datetime.now() - timedelta(days=20)).strftime('%Y-%m-%d %H:%M:%S')
        db.conn.execute(
            "INSERT INTO operation_logs (timestamp, category, action) VALUES (?, 'crawl', 'old')",
            (old_ts,)
        )
        db.conn.commit()
        # Insert a recent log
        db.insert_log('crawl', 'recent')
        # Cleanup logs older than 15 days
        deleted = db.cleanup_old_logs(days=15)
        assert deleted == 1
        logs = db.get_logs(page=1, per_page=10)
        assert logs['total'] == 1
        assert logs['logs'][0]['action'] == 'recent'

    def test_cleanup_old_logs_keeps_recent(self, db):
        from datetime import datetime, timedelta
        # Insert a log 5 days ago (within 15-day window)
        recent_ts = (datetime.now() - timedelta(days=5)).strftime('%Y-%m-%d %H:%M:%S')
        db.conn.execute(
            "INSERT INTO operation_logs (timestamp, category, action) VALUES (?, 'crawl', 'recent')",
            (recent_ts,)
        )
        db.conn.commit()
        deleted = db.cleanup_old_logs(days=15)
        assert deleted == 0
        logs = db.get_logs(page=1, per_page=10)
        assert logs['total'] == 1

    def test_cleanup_old_logs_empty_db(self, db):
        deleted = db.cleanup_old_logs(days=15)
        assert deleted == 0
