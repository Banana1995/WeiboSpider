#!/usr/bin/env python
# encoding: utf-8
"""
采集指定用户的所有推文（改造版）
"""
import json
from scrapy import Spider
from scrapy.http import Request
from spiders.common import parse_tweet_info, parse_long_tweet


class TweetSpiderByUserID(Spider):
    name = "tweet_spider_by_user_id"

    def __init__(self, user_ids=None, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.user_ids = user_ids or ['1087770692']
        if isinstance(self.user_ids, str):
            self.user_ids = [self.user_ids]

    def start_requests(self):
        for user_id in self.user_ids:
            url = (
                f"https://weibo.com/ajax/statuses/searchProfile?"
                f"uid={user_id}&page=1&hasori=1&hastext=1&haspic=1"
                f"&hasvideo=1&hasmusic=1&hasret=1"
            )
            yield Request(url, callback=self.parse, meta={'user_id': user_id, 'page_num': 1})

    def parse(self, response, **kwargs):
        data = json.loads(response.text)
        tweets = data.get('data', {}).get('list', [])
        user_id = response.meta['user_id']

        for tweet in tweets:
            item = parse_tweet_info(tweet)
            item['user_id'] = user_id
            del item['user']
            if item['isLongText']:
                url = "https://weibo.com/ajax/statuses/longtext?id=" + item['mblogid']
                yield Request(url, callback=parse_long_tweet, meta={'item': item})
            else:
                yield item

        if tweets:
            page_num = response.meta['page_num']
            url = response.url.replace(f'page={page_num}', f'page={page_num + 1}')
            yield Request(url, callback=self.parse, meta={'user_id': user_id, 'page_num': page_num + 1})
