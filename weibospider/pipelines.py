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
            is_retweet = int(item.get('is_retweet', False))
            user_id = str(item.get('user_id', ''))
            retweet_user_id = str(item.get('retweet_user_id', ''))
            if is_retweet == 0:
                item['deleted'] = 0
            elif is_retweet == 1:
                if retweet_user_id and retweet_user_id == user_id:
                    item['deleted'] = 0
                else:
                    item['deleted'] = 1
            else:
                item['deleted'] = 1
            self.db.insert_tweet(item)
        elif spider.name == 'comment':
            self.db.insert_comment(item)

        return item
