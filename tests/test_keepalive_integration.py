# tests/test_keepalive_integration.py
"""Tests for keepalive scheduler integration: periodic cookie refresh job."""
import os
import sys
import time
import pytest
from unittest.mock import patch, MagicMock

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))


@pytest.fixture
def scheduler():
    from scheduler import CrawlScheduler
    import keepalive
    calls = {'tweet': [], 'comment': [], 'keepalive': []}

    def mock_crawl_tweets(sch, mode='incremental', user_id=None):
        calls['tweet'].append(mode)

    def mock_crawl_comments(sch, mode='incremental'):
        calls['comment'].append(mode)

    # Pass a lambda so patching keepalive.refresh_cookie at call time works
    sch = CrawlScheduler(mock_crawl_tweets, mock_crawl_comments,
                         keepalive_func=lambda: keepalive.refresh_cookie(keepalive._test_db) if hasattr(keepalive, '_test_db') else keepalive.refresh_cookie())
    yield sch, calls
    sch.shutdown()


@pytest.fixture
def scheduler_with_mock_keepalive():
    from scheduler import CrawlScheduler
    calls = {'tweet': [], 'comment': [], 'keepalive': []}

    def mock_crawl_tweets(sch, mode='incremental', user_id=None):
        calls['tweet'].append(mode)

    def mock_crawl_comments(sch, mode='incremental'):
        calls['comment'].append(mode)

    mock_keepalive = MagicMock(return_value={'refreshed': True})
    sch = CrawlScheduler(mock_crawl_tweets, mock_crawl_comments,
                         keepalive_func=mock_keepalive)
    yield sch, calls, mock_keepalive
    sch.shutdown()


class TestKeepaliveScheduler:
    def test_keepalive_registered_when_schedule_enabled(self, scheduler_with_mock_keepalive):
        sch, calls, _ = scheduler_with_mock_keepalive
        sch.update_config({
            'schedule_enabled': True,
            'schedule_start_hour': 5,
            'schedule_end_hour': 23,
            'tweet_interval_minutes': 60,
            'comment_interval_minutes': 30,
        })
        job_ids = [job.id for job in sch._scheduler.get_jobs()]
        assert 'cookie_keepalive' in job_ids

    def test_keepalive_not_registered_when_schedule_disabled(self, scheduler_with_mock_keepalive):
        sch, calls, _ = scheduler_with_mock_keepalive
        sch.update_config({
            'schedule_enabled': False,
            'schedule_start_hour': 5,
            'schedule_end_hour': 23,
            'tweet_interval_minutes': 60,
            'comment_interval_minutes': 30,
        })
        job_ids = [job.id for job in sch._scheduler.get_jobs()]
        assert 'cookie_keepalive' not in job_ids

    def test_manual_keepalive_runs_refresh(self, scheduler_with_mock_keepalive):
        sch, calls, mock_keepalive = scheduler_with_mock_keepalive
        result = sch.manual_keepalive()
        assert result['status'] == 'ok'
        mock_keepalive.assert_called_once()

    def test_manual_keepalive_handles_error(self, scheduler_with_mock_keepalive):
        sch, calls, mock_keepalive = scheduler_with_mock_keepalive
        mock_keepalive.return_value = {'error': 'no cookie'}
        result = sch.manual_keepalive()
        assert result['status'] == 'error'
