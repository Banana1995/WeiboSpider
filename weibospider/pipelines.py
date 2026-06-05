# weibospider/pipelines.py
import time
from db import TweetDB


class SqlitePipeline:
    """Scrapy pipeline: write items to SQLite."""

    db = None

    @classmethod
    def from_crawler(cls, crawler):
        pipeline = cls()
        cls.db = TweetDB()
        return pipeline

    def process_item(self, item, spider):
        item['crawl_time'] = int(time.time())

        if spider.name == 'tweet_spider_by_user_id':
            self.db.insert_tweet(item)
        elif spider.name == 'comment':
            self.db.insert_comment(item)

        return item
