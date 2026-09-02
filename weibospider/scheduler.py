# weibospider/scheduler.py
import threading
import logging
from datetime import datetime

import pytz
from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.cron import CronTrigger
from apscheduler.triggers.interval import IntervalTrigger

logger = logging.getLogger(__name__)

# Fixed schedule (Beijing time): mimic human posting cadence, only inside the
# 07:00-22:00 window. Random 0-5 min delay is added per run (simulate a human
# who doesn't post at perfectly regular times).
SCHEDULE_TIMEZONE = 'Asia/Shanghai'
SCHEDULE_START_HOUR = 7
SCHEDULE_END_HOUR = 22  # exclusive: no new crawl starts at/after this hour
TWEET_INTERVAL_MINUTES = 62
COMMENT_INTERVAL_MINUTES = 47
SCHEDULE_JITTER_SECONDS = 5 * 60


def _beijing_now():
    """Current time in the Asia/Shanghai timezone."""
    return datetime.now(pytz.timezone(SCHEDULE_TIMEZONE))


class CrawlScheduler:
    def __init__(self, crawl_tweets_func, crawl_comments_func, keepalive_func=None, crawl_xueqiu_func=None, crawl_xueqiu_comments_func=None):
        self.crawl_tweets_func = crawl_tweets_func
        self.crawl_comments_func = crawl_comments_func
        self.keepalive_func = keepalive_func
        self.crawl_xueqiu_func = crawl_xueqiu_func
        self.crawl_xueqiu_comments_func = crawl_xueqiu_comments_func
        # Independent locks
        self._tweet_lock = threading.Lock()
        self._comment_lock = threading.Lock()
        # 全局串行锁：推文/评论抓取不并发。评论抓取必须在推文抓取完成（新微博入库）之后再选目标，
        # 否则并发时评论抓取看不到本次新抓到的微博（手动增量同步曾因此漏抓评论）。
        self._crawl_lock = threading.Lock()
        self._xq_lock = threading.Lock()
        self._xq_comment_lock = threading.Lock()
        self._tweet_running = False
        self._comment_running = False
        self._xq_running = False
        self._xq_comment_running = False
        self._tweet_cancelled = False
        self._comment_cancelled = False
        self._xq_cancelled = False
        self._xq_comment_cancelled = False
        self._tweet_last_result = None
        self._comment_last_result = None
        self._xq_last_result = None
        self._xq_comment_last_result = None
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
        """Update config dict and reload jobs when the enabled flag changes.

        The window hours / intervals are hard-coded constants now, so only the
        schedule_enabled toggle drives (re)building the jobs.
        """
        enabled_changed = (self._config.get('schedule_enabled')
                           != config.get('schedule_enabled'))
        self._config = config
        if enabled_changed:
            self._reload_jobs()

    def _reload_jobs(self):
        """Remove existing jobs and re-add based on current config."""
        for job_id in ('tweet_crawl', 'comment_crawl', 'cookie_keepalive', 'log_cleanup'):
            try:
                self._scheduler.remove_job(job_id)
            except Exception:
                pass
        if not self._is_schedule_enabled():
            logger.info("Schedule disabled, no jobs registered")
            return
        # Tweet / comment crawl on a fixed interval with a small random delay.
        # IntervalTrigger's first fire is one interval after registration, so
        # restarting/deploying never triggers an immediate crawl; a fire outside
        # the Beijing window is skipped by _within_window().
        self._scheduler.add_job(
            self._scheduled_tweet_crawl,
            IntervalTrigger(minutes=TWEET_INTERVAL_MINUTES,
                            jitter=SCHEDULE_JITTER_SECONDS,
                            timezone=SCHEDULE_TIMEZONE),
            id='tweet_crawl',
        )
        self._scheduler.add_job(
            self._scheduled_comment_crawl,
            IntervalTrigger(minutes=COMMENT_INTERVAL_MINUTES,
                            jitter=SCHEDULE_JITTER_SECONDS,
                            timezone=SCHEDULE_TIMEZONE),
            id='comment_crawl',
        )
        if self.keepalive_func is not None:
            self._scheduler.add_job(
                self._scheduled_keepalive,
                CronTrigger(hour=f'{SCHEDULE_START_HOUR}-{SCHEDULE_END_HOUR - 1}',
                            minute='0', timezone=SCHEDULE_TIMEZONE),
                id='cookie_keepalive',
            )
        # Daily log cleanup shortly after the window opens
        self._scheduler.add_job(
            self._scheduled_log_cleanup,
            CronTrigger(hour=SCHEDULE_START_HOUR, minute='5',
                        timezone=SCHEDULE_TIMEZONE),
            id='log_cleanup',
        )
        logger.info("Jobs registered: tweet every %dmin, comment every %dmin, "
                    "keepalive hourly, log_cleanup daily, Beijing %02d-%02d",
                    TWEET_INTERVAL_MINUTES, COMMENT_INTERVAL_MINUTES,
                    SCHEDULE_START_HOUR, SCHEDULE_END_HOUR)

    def _is_schedule_enabled(self):
        return self._config.get('schedule_enabled', False) is True

    def _within_window(self, now=None):
        """True when `now` (a datetime, Beijing time by default) falls inside
        the fixed schedule window [SCHEDULE_START_HOUR, SCHEDULE_END_HOUR)."""
        if now is None:
            now = _beijing_now()
        return SCHEDULE_START_HOUR <= now.hour < SCHEDULE_END_HOUR

    def _log_schedule(self, category, action, status=None, detail=None):
        """Insert an operation log via DB if available (set by create_app)."""
        log_func = getattr(self, '_log_to_db', None)
        if log_func:
            try:
                log_func(category, action, detail=detail, status=status)
            except Exception:
                pass

    def _scheduled_tweet_crawl(self):
        if not self._within_window():
            logger.info("Scheduled tweet crawl skipped: outside Beijing window %02d-%02d",
                        SCHEDULE_START_HOUR, SCHEDULE_END_HOUR)
            self._log_schedule('crawl', 'scheduled_tweet_crawl', status='skipped',
                               detail='outside Beijing schedule window')
            return
        logger.info("Scheduled tweet crawl triggered")
        self._log_schedule('crawl', 'scheduled_tweet_crawl')
        self._execute_tweet_job(mode='incremental')

    def _scheduled_comment_crawl(self):
        if not self._within_window():
            logger.info("Scheduled comment crawl skipped: outside Beijing window %02d-%02d",
                        SCHEDULE_START_HOUR, SCHEDULE_END_HOUR)
            self._log_schedule('crawl', 'scheduled_comment_crawl', status='skipped',
                               detail='outside Beijing schedule window')
            return
        logger.info("Scheduled comment crawl triggered")
        self._log_schedule('crawl', 'scheduled_comment_crawl')
        self._execute_comment_job(mode='incremental')

    def _scheduled_keepalive(self):
        logger.info("Scheduled cookie keepalive triggered")
        # Only run if ALF expiry is within 3 days
        from keepalive import should_keepalive_now
        cookie = ''
        try:
            cookie = self._get_cookie() if hasattr(self, '_get_cookie') else ''
        except Exception:
            pass
        if not should_keepalive_now(cookie):
            logger.info("Cookie keepalive skipped: ALF not expiring soon")
            self._log_schedule('keepalive', 'keepalive_skipped',
                               status='skipped', detail='ALF not expiring soon')
            return
        self._log_schedule('keepalive', 'scheduled_keepalive')
        self.manual_keepalive()

    def _scheduled_log_cleanup(self):
        """Daily cleanup: delete operation logs older than 15 days."""
        logger.info("Scheduled log cleanup triggered")
        cleanup_func = getattr(self, '_cleanup_old_logs', None)
        if cleanup_func is None:
            return
        try:
            deleted = cleanup_func(days=15)
            logger.info("Log cleanup: deleted %d old logs", deleted)
            self._log_schedule('schedule', 'log_cleanup',
                               status='success', detail=f'deleted={deleted}')
        except Exception as e:
            logger.error("Log cleanup failed: %s", e)
            self._log_schedule('schedule', 'log_cleanup',
                               status='failed', detail=str(e)[:200])

    def manual_keepalive(self):
        """Run cookie keepalive once, return result dict."""
        if self.keepalive_func is None:
            return {'error': 'keepalive not configured'}
        try:
            result = self.keepalive_func()
            self._keepalive_last_result = result
            logger.info("Cookie keepalive finished: %s", result)
            status = 'success' if 'error' not in result else 'failed'
            self._log_schedule('keepalive', 'keepalive_result',
                               status=status,
                               detail=str(result)[:200])
            if 'error' in result:
                return {'status': 'error', 'error': result['error'], 'result': result}
            return {'status': 'ok', 'result': result}
        except Exception as e:
            self._keepalive_last_result = {'error': str(e)}
            logger.error("Cookie keepalive failed: %s", e)
            self._log_schedule('keepalive', 'keepalive_result',
                               status='failed', detail=str(e)[:200])
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
        """Incremental crawl: tweets then comments sequentially (comments must see the new tweets)."""
        if self._tweet_running or self._comment_running:
            return {'status': 'rejected', 'message': '已有抓取任务在运行'}
        self._tweet_cancelled = False
        self._comment_cancelled = False
        threading.Thread(target=self._execute_incremental, daemon=True).start()
        return {'status': 'started', 'message': '增量同步已启动'}

    def manual_xueqiu(self, mode='full'):
        """Crawl xueqiu user timeline. mode='full' or 'incremental'."""
        if self.crawl_xueqiu_func is None:
            return {'status': 'error', 'message': '雪球抓取未配置'}
        if self._xq_running:
            return {'status': 'rejected', 'message': '雪球抓取正在运行中'}
        self._xq_cancelled = False
        threading.Thread(target=self._execute_xueqiu_job, kwargs={'mode': mode}, daemon=True).start()
        return {'status': 'started', 'message': '雪球同步已启动'}

    def manual_xueqiu_comments(self, mode='ps'):
        """Crawl comments for xueqiu posts. mode='ps' (PS图 posts) or 'all'."""
        if self.crawl_xueqiu_comments_func is None:
            return {'status': 'error', 'message': '雪球评论抓取未配置'}
        if self._xq_comment_running:
            return {'status': 'rejected', 'message': '雪球评论抓取正在运行中'}
        self._xq_comment_cancelled = False
        threading.Thread(target=self._execute_xueqiu_comment_job, kwargs={'mode': mode}, daemon=True).start()
        return {'status': 'started', 'message': '雪球评论抓取已启动'}

    def cancel(self):
        if not (self._tweet_running or self._comment_running or self._xq_running or self._xq_comment_running):
            return {'status': 'error', 'message': '没有正在运行的抓取任务'}
        self._tweet_cancelled = True
        self._comment_cancelled = True
        self._xq_cancelled = True
        self._xq_comment_cancelled = True
        logger.info("Cancelling all crawl tasks...")
        return {'status': 'cancelling', 'message': '正在取消抓取...'}

    @property
    def tweet_cancelled(self):
        return self._tweet_cancelled

    @property
    def comment_cancelled(self):
        return self._comment_cancelled

    @property
    def xueqiu_cancelled(self):
        return self._xq_cancelled

    @property
    def xueqiu_comment_cancelled(self):
        return self._xq_comment_cancelled

    def _execute_xueqiu_job(self, mode='full'):
        if not self._xq_lock.acquire(blocking=False):
            logger.warning("Xueqiu crawl already running, skip")
            return
        try:
            self._xq_running = True
            self._xq_cancelled = False
            self._xq_last_result = None
            result = self.crawl_xueqiu_func(self, mode=mode)
            self._xq_last_result = result
            logger.info("Xueqiu crawl finished: %s", result)
        except Exception as e:
            self._xq_last_result = {'error': str(e)}
            logger.error("Xueqiu crawl failed: %s", e)
        finally:
            self._xq_running = False
            self._xq_lock.release()

    def _execute_xueqiu_comment_job(self, mode='ps'):
        if not self._xq_comment_lock.acquire(blocking=False):
            logger.warning("Xueqiu comment crawl already running, skip")
            return
        try:
            self._xq_comment_running = True
            self._xq_comment_cancelled = False
            self._xq_comment_last_result = None
            result = self.crawl_xueqiu_comments_func(self, mode=mode)
            self._xq_comment_last_result = result
            logger.info("Xueqiu comment crawl finished: %s", result)
        except Exception as e:
            self._xq_comment_last_result = {'error': str(e)}
            logger.error("Xueqiu comment crawl failed: %s", e)
        finally:
            self._xq_comment_running = False
            self._xq_comment_lock.release()

    def _execute_full(self, user_id=None):
        """Run tweets then comments in full mode, sequentially."""
        self._execute_tweet_job(mode='full', user_id=user_id)
        self._execute_comment_job(mode='full')

    def _execute_incremental(self):
        """Run tweets then comments in incremental mode, sequentially.

        顺序保证：评论抓取选目标（get_tweets_for_comment_crawl）时，本次推文抓取的
        新微博已经写入数据库，否则并发会导致新微博的评论漏抓。
        """
        self._execute_tweet_job(mode='incremental')
        self._execute_comment_job(mode='incremental')

    def _execute_tweet_job(self, mode='incremental', user_id=None):
        if not self._tweet_lock.acquire(blocking=False):
            logger.warning("Tweet crawl already running, skip")
            return
        try:
            with self._crawl_lock:
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
            with self._crawl_lock:
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
            'xueqiu': {
                'running': self._xq_running,
                'last_result': self._xq_last_result,
            },
            'xueqiu_comment': {
                'running': self._xq_comment_running,
                'last_result': self._xq_comment_last_result,
            },
        }

    def clear_last_results(self):
        """Forget the previous weibo tweet/comment crawl outcomes.

        Called when a new weibo cookie is saved so a stale 'Cookie 已过期'
        failure no longer keeps /api/crawl/status reporting cookie_expired
        until the next crawl of that type runs. Xueqiu results are untouched
        (they use a separate cookie).
        """
        self._tweet_last_result = None
        self._comment_last_result = None

    @property
    def cancelled(self):
        """Legacy: return True if either is cancelled."""
        return self._tweet_cancelled or self._comment_cancelled
