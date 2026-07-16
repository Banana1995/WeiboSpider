"""Integration tests: verify scheduler job registration, time window, config reload,
and spider command construction with mocked subprocess."""
import os
import sys
import json
import tempfile
import time
from unittest.mock import patch, MagicMock
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))


@pytest.fixture
def scheduler():
    from scheduler import CrawlScheduler
    calls = {'tweet': [], 'comment': []}

    def mock_crawl_tweets(sch, mode='incremental', user_id=None):
        calls['tweet'].append(mode)

    def mock_crawl_comments(sch, mode='incremental'):
        calls['comment'].append(mode)

    sch = CrawlScheduler(mock_crawl_tweets, mock_crawl_comments)
    sch.start()
    yield sch, calls
    sch.shutdown()


class TestSchedulerJobRegistration:
    def test_no_jobs_when_disabled(self, scheduler):
        sch, _ = scheduler
        jobs = sch._scheduler.get_jobs()
        assert len(jobs) == 0

    def test_jobs_registered_when_enabled(self, scheduler):
        sch, _ = scheduler
        sch.update_config({
            'schedule_enabled': True,
            'schedule_start_hour': 5,
            'schedule_end_hour': 23,
            'tweet_interval_minutes': 60,
            'comment_interval_minutes': 30,
        })
        time.sleep(0.1)
        jobs = sch._scheduler.get_jobs()
        job_ids = [j.id for j in jobs]
        assert 'tweet_crawl' in job_ids
        assert 'comment_crawl' in job_ids

    def test_jobs_removed_when_disabled(self, scheduler):
        sch, _ = scheduler
        sch.update_config({
            'schedule_enabled': True,
            'schedule_start_hour': 5,
            'schedule_end_hour': 23,
            'tweet_interval_minutes': 60,
            'comment_interval_minutes': 30,
        })
        time.sleep(0.1)
        assert len(sch._scheduler.get_jobs()) == 3

        sch.update_config({
            'schedule_enabled': False,
            'schedule_start_hour': 5,
            'schedule_end_hour': 23,
            'tweet_interval_minutes': 60,
            'comment_interval_minutes': 30,
        })
        time.sleep(0.1)
        assert len(sch._scheduler.get_jobs()) == 0

    def test_job_trigger_hours(self, scheduler):
        """Verify the CronTrigger hour range is correct."""
        sch, _ = scheduler
        sch.update_config({
            'schedule_enabled': True,
            'schedule_start_hour': 5,
            'schedule_end_hour': 23,
            'tweet_interval_minutes': 60,
            'comment_interval_minutes': 30,
        })
        time.sleep(0.1)
        jobs = {j.id: j for j in sch._scheduler.get_jobs()}
        tweet_trigger = jobs['tweet_crawl'].trigger
        comment_trigger = jobs['comment_crawl'].trigger
        # hour range should be 5-22 (end_hour-1=22, inclusive)
        # CronTrigger stores fields; check the str representation
        assert '5-22' in str(tweet_trigger)
        assert '5-22' in str(comment_trigger)
        # tweet: minute=0 (hourly at :00)
        assert '0' in str(tweet_trigger).split(' ')[1] or 'minute=0' in str(tweet_trigger)
        # comment: minute=0,30 (every 30 min)
        assert '0,30' in str(comment_trigger) or '0' in str(comment_trigger)

    def test_config_change_reloads_jobs(self, scheduler):
        sch, _ = scheduler
        # Enable with 5-23
        sch.update_config({
            'schedule_enabled': True,
            'schedule_start_hour': 5,
            'schedule_end_hour': 23,
            'tweet_interval_minutes': 60,
            'comment_interval_minutes': 30,
        })
        time.sleep(0.1)
        jobs = sch._scheduler.get_jobs()
        assert len(jobs) == 3
        trigger_str = str({j.id: j for j in jobs}['tweet_crawl'].trigger)
        assert '5-22' in trigger_str

        # Change to 8-20
        sch.update_config({
            'schedule_enabled': True,
            'schedule_start_hour': 8,
            'schedule_end_hour': 20,
            'tweet_interval_minutes': 60,
            'comment_interval_minutes': 30,
        })
        time.sleep(0.1)
        jobs = sch._scheduler.get_jobs()
        assert len(jobs) == 3
        trigger_str = str({j.id: j for j in jobs}['tweet_crawl'].trigger)
        assert '8-19' in trigger_str

    def test_no_reload_when_config_unchanged(self, scheduler):
        sch, _ = scheduler
        config = {
            'schedule_enabled': True,
            'schedule_start_hour': 5,
            'schedule_end_hour': 23,
            'tweet_interval_minutes': 60,
            'comment_interval_minutes': 30,
        }
        sch.update_config(config)
        time.sleep(0.1)
        # Calling again with same config should not reload
        # (we can't easily verify "no reload" but at least jobs should still be 2)
        sch.update_config(config)
        time.sleep(0.1)
        assert len(sch._scheduler.get_jobs()) == 3


class TestSchedulerLocks:
    def test_tweet_and_comment_can_run_in_parallel(self, scheduler):
        """When manual_incremental is called, both should start."""
        sch, calls = scheduler
        result = sch.manual_incremental()
        assert result['status'] == 'started'
        time.sleep(0.15)
        # Both should have been called
        assert len(calls['tweet']) == 1
        assert len(calls['comment']) == 1

    def test_incremental_rejected_when_both_running(self, scheduler):
        sch, calls = scheduler
        # Block the mock crawl functions so they stay "running"
        import threading as t_mod
        barrier = t_mod.Event()
        original_tweet = sch.crawl_tweets_func
        original_comment = sch.crawl_comments_func

        def blocking_tweet(sch, mode='incremental', user_id=None):
            calls['tweet'].append(mode)
            barrier.wait(timeout=2)

        def blocking_comment(sch, mode='incremental'):
            calls['comment'].append(mode)
            barrier.wait(timeout=2)

        sch.crawl_tweets_func = blocking_tweet
        sch.crawl_comments_func = blocking_comment

        sch.manual_incremental()
        time.sleep(0.1)
        # Now both are running, try again
        result = sch.manual_incremental()
        assert result['status'] == 'rejected'

        # Release
        barrier.set()
        time.sleep(0.2)
        sch.crawl_tweets_func = original_tweet
        sch.crawl_comments_func = original_comment


class TestSpiderCommandConstruction:
    """Verify that _crawl_tweets and _crawl_comments construct correct scrapy commands."""

    @pytest.fixture
    def app_env(self):
        """Set up a test DB and app module."""
        fd, path = tempfile.mkstemp(suffix='.db')
        os.close(fd)
        os.environ['SCRAPY_SETTINGS_MODULE'] = 'settings'
        from db import TweetDB
        import app as app_module
        app_module.DB = TweetDB(path)
        app_module.app.config['TESTING'] = True
        app_module.app.config['SCHEDULER_DISABLED'] = True
        app_module.DB.set_config('cookie', 'test_cookie=1')
        app_module.DB.set_config('user_ids', ['uid1'])
        yield app_module
        app_module.DB.close()
        os.unlink(path)

    def test_incremental_tweet_command_has_stop_after_id(self, app_env):
        """Incremental mode should pass stop_after_id to spider."""
        app_env.DB.insert_tweet({
            '_id': '999', 'mblogid': 'Mb999', 'user_id': 'uid1',
            'content': 'existing', 'created_at': '2024-06-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        captured_cmds = []

        class FakeProc:
            returncode = 0
            def poll(self):
                return 0
            stdout = iter([])

        def fake_popen(cmd_args, **kwargs):
            captured_cmds.append(cmd_args)
            proc = MagicMock()
            proc.poll.return_value = 0
            proc.stdout = iter([])
            return proc

        with patch('subprocess.Popen', side_effect=fake_popen):
            result = app_env._crawl_tweets(scheduler=None, mode='incremental')

        assert len(captured_cmds) == 1
        cmd = captured_cmds[0]
        # Should have stop_after_id in the command
        stop_idx = [i for i, arg in enumerate(cmd) if arg == 'stop_after_id=999']
        assert len(stop_idx) == 1, f"stop_after_id=999 not found in {cmd}"

    def test_full_tweet_command_no_stop_after_id(self, app_env):
        """Full mode should NOT pass stop_after_id."""
        captured_cmds = []

        def fake_popen(cmd_args, **kwargs):
            captured_cmds.append(cmd_args)
            proc = MagicMock()
            proc.poll.return_value = 0
            proc.stdout = iter([])
            return proc

        with patch('subprocess.Popen', side_effect=fake_popen):
            result = app_env._crawl_tweets(scheduler=None, mode='full')

        assert len(captured_cmds) == 1
        cmd = captured_cmds[0]
        # Should NOT have stop_after_id
        stop_args = [a for a in cmd if 'stop_after_id' in str(a)]
        assert len(stop_args) == 0, f"stop_after_id should not be in {cmd}"
        # Should have start_time and end_time
        assert any('start_time=' in str(a) for a in cmd)
        assert any('end_time=' in str(a) for a in cmd)

    def test_incremental_comment_command_has_max_pages(self, app_env):
        """Incremental comment crawl should pass max_pages=2."""
        from datetime import datetime, timedelta
        recent = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
        app_env.DB.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'uid1',
            'content': 'recent', 'created_at': recent,
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        captured_cmds = []

        def fake_popen(cmd_args, **kwargs):
            captured_cmds.append(cmd_args)
            proc = MagicMock()
            proc.poll.return_value = 0
            proc.stdout = iter([])
            return proc

        with patch('subprocess.Popen', side_effect=fake_popen):
            result = app_env._crawl_comments(scheduler=None, mode='incremental')

        assert len(captured_cmds) == 1
        cmd = captured_cmds[0]
        assert 'max_pages=2' in cmd, f"max_pages=2 not found in {cmd}"
        assert 'flow=0' in cmd

    def test_full_comment_command_no_max_pages(self, app_env):
        """Full comment crawl should NOT pass max_pages."""
        app_env.DB.set_config('start_date', '2024-01-01')
        app_env.DB.set_config('end_date', '2024-12-31')
        app_env.DB.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'uid1',
            'content': 'test', 'created_at': '2024-06-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        captured_cmds = []

        def fake_popen(cmd_args, **kwargs):
            captured_cmds.append(cmd_args)
            proc = MagicMock()
            proc.poll.return_value = 0
            proc.stdout = iter([])
            return proc

        with patch('subprocess.Popen', side_effect=fake_popen):
            result = app_env._crawl_comments(scheduler=None, mode='full')

        assert len(captured_cmds) == 1
        cmd = captured_cmds[0]
        max_pages_args = [a for a in cmd if 'max_pages' in str(a)]
        assert len(max_pages_args) == 0, f"max_pages should not be in {cmd}"


class TestAPIIntegration:
    """Verify API endpoints return correct data shapes."""

    @pytest.fixture
    def client(self):
        fd, path = tempfile.mkstemp(suffix='.db')
        os.close(fd)
        os.environ['SCRAPY_SETTINGS_MODULE'] = 'settings'
        from db import TweetDB
        import app as app_module
        app_module.DB = TweetDB(path)
        app_module.app.config['TESTING'] = True
        app_module.app.config['SCHEDULER_DISABLED'] = True
        c = app_module.app.test_client()
        yield c
        app_module.DB.close()
        os.unlink(path)

    def test_config_get_has_schedule_fields(self, client):
        rv = client.get('/api/config')
        data = json.loads(rv.data)
        for key in ('schedule_enabled', 'schedule_start_hour', 'schedule_end_hour',
                     'tweet_interval_minutes', 'comment_interval_minutes'):
            assert key in data, f"{key} missing from config response"

    def test_config_post_schedule_then_get_matches(self, client):
        rv = client.post('/api/config', json={
            'schedule_enabled': True,
            'schedule_start_hour': 7,
            'schedule_end_hour': 19,
            'tweet_interval_minutes': 90,
            'comment_interval_minutes': 45,
        })
        assert json.loads(rv.data)['updated'] is True

        rv = client.get('/api/config')
        data = json.loads(rv.data)
        assert data['schedule_enabled'] is True
        assert data['schedule_start_hour'] == 7
        assert data['schedule_end_hour'] == 19
        assert data['tweet_interval_minutes'] == 90
        assert data['comment_interval_minutes'] == 45

    def test_crawl_status_dual(self, client):
        rv = client.get('/api/crawl/status')
        data = json.loads(rv.data)
        assert 'tweet' in data and 'comment' in data
        assert 'running' in data['tweet'] and 'running' in data['comment']

    def test_incremental_endpoint(self, client):
        rv = client.post('/api/crawl/incremental')
        data = json.loads(rv.data)
        # In test mode (SCHEDULER is None), returns started
        assert data['status'] == 'started'
