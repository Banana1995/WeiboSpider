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
