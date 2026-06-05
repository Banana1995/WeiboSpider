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

    def manual_crawl(self):
        if self._running:
            return {'status': 'rejected', 'message': '已有抓取任务在运行'}
        # Run in background thread so API can return immediately
        t = threading.Thread(target=self._execute, daemon=True)
        t.start()
        return {'status': 'started', 'message': '抓取已启动'}

    def _execute(self):
        if not self._lock.acquire(blocking=False):
            logger.warning("Crawl already running, skip")
            return
        try:
            self._running = True
            self._last_result = None
            logger.info("Crawl started")
            result = self.crawl_func()
            self._last_result = result
            logger.info(f"Crawl finished: {result}")
        except Exception as e:
            self._last_result = {'error': str(e)}
            logger.error(f"Crawl failed: {e}")
        finally:
            self._running = False
            self._lock.release()

    @property
    def status(self):
        return {
            'running': self._running,
            'last_result': self._last_result,
        }
