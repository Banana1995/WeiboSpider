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

    def test_manual_incremental_runs_tweet_before_comment(self, scheduler):
        sch, calls = scheduler
        order = []
        sch.crawl_tweets_func = lambda sch, mode='incremental', user_id=None: order.append('tweet')
        sch.crawl_comments_func = lambda sch, mode='incremental': order.append('comment')
        result = sch.manual_incremental()
        assert result['status'] == 'started'
        time.sleep(0.3)
        assert order == ['tweet', 'comment']

    def test_manual_xueqiu(self, scheduler):
        sch, calls = scheduler
        xq_calls = []
        sch.crawl_xueqiu_func = lambda sch, mode='full': xq_calls.append(mode)
        result = sch.manual_xueqiu(mode='full')
        assert result['status'] == 'started'
        time.sleep(0.3)
        assert xq_calls == ['full']
        assert sch.status['xueqiu']['running'] is False

    def test_manual_xueqiu_unconfigured(self, scheduler):
        sch, calls = scheduler
        result = sch.manual_xueqiu()
        assert result['status'] == 'error'

    def test_manual_xueqiu_comments(self, scheduler):
        sch, calls = scheduler
        xq_calls = []
        sch.crawl_xueqiu_comments_func = lambda sch, mode='ps': xq_calls.append(mode)
        result = sch.manual_xueqiu_comments(mode='ps')
        assert result['status'] == 'started'
        time.sleep(0.3)
        assert xq_calls == ['ps']
        assert sch.status['xueqiu_comment']['running'] is False

    def test_manual_xueqiu_comments_unconfigured(self, scheduler):
        sch, calls = scheduler
        result = sch.manual_xueqiu_comments()
        assert result['status'] == 'error'

    def test_manual_crawl_rejected_when_running(self, scheduler):
        sch, calls = scheduler
        sch.manual_crawl()
        time.sleep(0.05)
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
        assert sch._is_schedule_enabled() is False


class TestScheduleWindow:
    """Scheduled crawls only run inside the fixed Beijing window (7-22)."""

    def _make(self):
        from scheduler import CrawlScheduler
        calls = []
        sch = CrawlScheduler(
            lambda sch, mode='incremental', user_id=None: calls.append('tweet:' + mode),
            lambda sch, mode='incremental': calls.append('comment:' + mode))
        return sch, calls

    def test_within_window_boundaries(self):
        from datetime import datetime
        sch, _ = self._make()
        assert sch._within_window(datetime(2026, 9, 2, 7, 0)) is True
        assert sch._within_window(datetime(2026, 9, 2, 21, 59)) is True
        assert sch._within_window(datetime(2026, 9, 2, 22, 0)) is False
        assert sch._within_window(datetime(2026, 9, 2, 6, 59)) is False

    def test_scheduled_tweet_skipped_outside_window(self, monkeypatch):
        from datetime import datetime
        sch, calls = self._make()
        monkeypatch.setattr('scheduler._beijing_now',
                            lambda: datetime(2026, 9, 2, 23, 0))
        sch._scheduled_tweet_crawl()
        assert calls == []

    def test_scheduled_tweet_runs_inside_window(self, monkeypatch):
        from datetime import datetime
        sch, calls = self._make()
        monkeypatch.setattr('scheduler._beijing_now',
                            lambda: datetime(2026, 9, 2, 10, 0))
        sch._scheduled_tweet_crawl()
        assert calls == ['tweet:incremental']

    def test_scheduled_comment_skipped_outside_window(self, monkeypatch):
        from datetime import datetime
        sch, calls = self._make()
        monkeypatch.setattr('scheduler._beijing_now',
                            lambda: datetime(2026, 9, 2, 22, 5))
        sch._scheduled_comment_crawl()
        assert calls == []

    def test_scheduled_comment_runs_inside_window(self, monkeypatch):
        from datetime import datetime
        sch, calls = self._make()
        monkeypatch.setattr('scheduler._beijing_now',
                            lambda: datetime(2026, 9, 2, 8, 30))
        sch._scheduled_comment_crawl()
        assert calls == ['comment:incremental']
