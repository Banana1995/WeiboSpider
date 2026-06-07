# weibospider/scheduler.py
import threading
import logging

from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.cron import CronTrigger

logger = logging.getLogger(__name__)


class CrawlScheduler:
    def __init__(self, crawl_func):
        self.crawl_func = crawl_func
        self._lock = threading.Lock()
        self._running = False
        self._cancelled = False
        self._last_result = None
        self._scheduler = BackgroundScheduler()
        self._scheduler.add_job(
            self._scheduled_crawl,
            CronTrigger(hour=2, minute=0),
            id='daily_crawl',
        )

    def start(self):
        self._scheduler.start()
        logger.info("Scheduler started, daily crawl at 02:00")

    def shutdown(self):
        self._scheduler.shutdown(wait=False)
        logger.info("Scheduler shutdown")

    def _scheduled_crawl(self):
        logger.info("Scheduled crawl triggered")
        self._execute()

    def manual_crawl(self, user_id=None):
        if self._running:
            return {'status': 'rejected', 'message': '已有抓取任务在运行'}
        self._cancelled = False
        t = threading.Thread(target=self._execute, args=(user_id,), daemon=True)
        t.start()
        return {'status': 'started', 'message': f'抓取已启动 ({"用户 "+user_id if user_id else "全部用户"})'}

    def cancel(self):
        if not self._running:
            return {'status': 'error', 'message': '没有正在运行的抓取任务'}
        self._cancelled = True
        logger.info("Cancelling crawl...")
        return {'status': 'cancelling', 'message': '正在取消抓取...'}

    @property
    def cancelled(self):
        return self._cancelled

    def _execute(self, user_id=None):
        if not self._lock.acquire(blocking=False):
            logger.warning("Crawl already running, skip")
            return
        try:
            self._running = True
            self._cancelled = False
            self._last_result = None
            logger.info("Crawl started (user=%s)", user_id or 'all')
            result = self.crawl_func(self, user_id=user_id)
            self._last_result = result
            logger.info("Crawl finished: %s", result)
        except Exception as e:
            self._last_result = {'error': str(e)}
            logger.error("Crawl failed: %s", e)
        finally:
            self._running = False
            self._lock.release()

    @property
    def status(self):
        return {
            'running': self._running,
            'last_result': self._last_result,
        }
