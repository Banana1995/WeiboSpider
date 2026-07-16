# weibospider/scheduler.py
import threading
import logging

from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.cron import CronTrigger

logger = logging.getLogger(__name__)


class CrawlScheduler:
    def __init__(self, crawl_tweets_func, crawl_comments_func, keepalive_func=None):
        self.crawl_tweets_func = crawl_tweets_func
        self.crawl_comments_func = crawl_comments_func
        self.keepalive_func = keepalive_func
        # Independent locks
        self._tweet_lock = threading.Lock()
        self._comment_lock = threading.Lock()
        self._tweet_running = False
        self._comment_running = False
        self._tweet_cancelled = False
        self._comment_cancelled = False
        self._tweet_last_result = None
        self._comment_last_result = None
        self._keepalive_last_result = None
        self._config = {}
        self._scheduler = BackgroundScheduler()

    def start(self):
        self._reload_jobs()
        self._scheduler.start()
        logger.info("Scheduler started")

    def shutdown(self):
        try:
            self._scheduler.shutdown(wait=False)
        except Exception:
            pass
        logger.info("Scheduler shutdown")

    def update_config(self, config):
        """Update config dict and reload jobs if schedule params changed."""
        old_keys = {k: self._config.get(k) for k in
                    ('schedule_enabled', 'schedule_start_hour', 'schedule_end_hour',
                     'tweet_interval_minutes', 'comment_interval_minutes')}
        self._config = config
        new_keys = {k: self._config.get(k) for k in old_keys}
        if old_keys != new_keys:
            self._reload_jobs()

    def _reload_jobs(self):
        """Remove existing jobs and re-add based on current config."""
        for job_id in ('tweet_crawl', 'comment_crawl', 'cookie_keepalive'):
            try:
                self._scheduler.remove_job(job_id)
            except Exception:
                pass
        if not self._is_schedule_enabled():
            logger.info("Schedule disabled, no jobs registered")
            return
        start_hour = self._config.get('schedule_start_hour', 5)
        end_hour = self._config.get('schedule_end_hour', 23)
        tweet_interval = self._config.get('tweet_interval_minutes', 60)
        comment_interval = self._config.get('comment_interval_minutes', 30)
        self._scheduler.add_job(
            self._scheduled_tweet_crawl,
            CronTrigger(hour=f'{start_hour}-{end_hour - 1}', minute='0'),
            id='tweet_crawl',
        )
        self._scheduler.add_job(
            self._scheduled_comment_crawl,
            CronTrigger(hour=f'{start_hour}-{end_hour - 1}', minute='0,30'),
            id='comment_crawl',
        )
        if self.keepalive_func is not None:
            self._scheduler.add_job(
                self._scheduled_keepalive,
                CronTrigger(hour=f'{start_hour}-{end_hour - 1}', minute='15,45'),
                id='cookie_keepalive',
            )
        logger.info("Jobs registered: tweet hourly, comment every 30min, keepalive every 30min, %dh-%dh",
                     start_hour, end_hour)

    def _is_schedule_enabled(self):
        return self._config.get('schedule_enabled', False) is True

    def _scheduled_tweet_crawl(self):
        logger.info("Scheduled tweet crawl triggered")
        self._execute_tweet_job(mode='incremental')

    def _scheduled_comment_crawl(self):
        logger.info("Scheduled comment crawl triggered")
        self._execute_comment_job(mode='incremental')

    def _scheduled_keepalive(self):
        logger.info("Scheduled cookie keepalive triggered")
        self.manual_keepalive()

    def manual_keepalive(self):
        """Run cookie keepalive once, return result dict."""
        if self.keepalive_func is None:
            return {'error': 'keepalive not configured'}
        try:
            result = self.keepalive_func()
            self._keepalive_last_result = result
            logger.info("Cookie keepalive finished: %s", result)
            if 'error' in result:
                return {'status': 'error', 'error': result['error'], 'result': result}
            return {'status': 'ok', 'result': result}
        except Exception as e:
            self._keepalive_last_result = {'error': str(e)}
            logger.error("Cookie keepalive failed: %s", e)
            return {'status': 'error', 'error': str(e)}

    def manual_crawl(self, user_id=None):
        """Full crawl: tweets + comments sequentially."""
        if self._tweet_running or self._comment_running:
            return {'status': 'rejected', 'message': '已有抓取任务在运行'}
        self._tweet_cancelled = False
        self._comment_cancelled = False
        t = threading.Thread(target=self._execute_full, args=(user_id,), daemon=True)
        t.start()
        return {'status': 'started', 'message': f'全量抓取已启动 ({"用户 " + user_id if user_id else "全部用户"})'}

    def manual_incremental(self):
        """Incremental crawl: tweets + comments in parallel."""
        if self._tweet_running and self._comment_running:
            return {'status': 'rejected', 'message': '推文和评论抓取均在运行中'}
        if self._tweet_running:
            return {'status': 'rejected', 'message': '推文抓取正在运行中'}
        if self._comment_running:
            return {'status': 'rejected', 'message': '评论抓取正在运行中'}
        self._tweet_cancelled = False
        self._comment_cancelled = False
        threading.Thread(target=self._execute_tweet_job, kwargs={'mode': 'incremental'}, daemon=True).start()
        threading.Thread(target=self._execute_comment_job, kwargs={'mode': 'incremental'}, daemon=True).start()
        return {'status': 'started', 'message': '增量同步已启动'}

    def cancel(self):
        if not self._tweet_running and not self._comment_running:
            return {'status': 'error', 'message': '没有正在运行的抓取任务'}
        self._tweet_cancelled = True
        self._comment_cancelled = True
        logger.info("Cancelling all crawl tasks...")
        return {'status': 'cancelling', 'message': '正在取消抓取...'}

    @property
    def tweet_cancelled(self):
        return self._tweet_cancelled

    @property
    def comment_cancelled(self):
        return self._comment_cancelled

    def _execute_full(self, user_id=None):
        """Run tweets then comments in full mode, sequentially."""
        self._execute_tweet_job(mode='full', user_id=user_id)
        self._execute_comment_job(mode='full')

    def _execute_tweet_job(self, mode='incremental', user_id=None):
        if not self._tweet_lock.acquire(blocking=False):
            logger.warning("Tweet crawl already running, skip")
            return
        try:
            self._tweet_running = True
            self._tweet_cancelled = False
            self._tweet_last_result = None
            result = self.crawl_tweets_func(self, mode=mode, user_id=user_id)
            self._tweet_last_result = result
            logger.info("Tweet crawl finished: %s", result)
        except Exception as e:
            self._tweet_last_result = {'error': str(e)}
            logger.error("Tweet crawl failed: %s", e)
        finally:
            self._tweet_running = False
            self._tweet_lock.release()

    def _execute_comment_job(self, mode='incremental'):
        if not self._comment_lock.acquire(blocking=False):
            logger.warning("Comment crawl already running, skip")
            return
        try:
            self._comment_running = True
            self._comment_cancelled = False
            self._comment_last_result = None
            result = self.crawl_comments_func(self, mode=mode)
            self._comment_last_result = result
            logger.info("Comment crawl finished: %s", result)
        except Exception as e:
            self._comment_last_result = {'error': str(e)}
            logger.error("Comment crawl failed: %s", e)
        finally:
            self._comment_running = False
            self._comment_lock.release()

    @property
    def status(self):
        return {
            'tweet': {
                'running': self._tweet_running,
                'last_result': self._tweet_last_result,
            },
            'comment': {
                'running': self._comment_running,
                'last_result': self._comment_last_result,
            },
        }

    @property
    def cancelled(self):
        """Legacy: return True if either is cancelled."""
        return self._tweet_cancelled or self._comment_cancelled
