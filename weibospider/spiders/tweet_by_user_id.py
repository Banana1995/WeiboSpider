#!/usr/bin/env python
# encoding: utf-8
"""
采集指定用户的所有推文（改造版）
支持时间范围过滤: start_time=2024-01-01, end_time=2024-12-31
"""
import datetime
import json
from scrapy import Spider
from scrapy.http import Request
from spiders.common import parse_tweet_info, parse_long_tweet


class TweetSpiderByUserID(Spider):
    name = "tweet_spider_by_user_id"

    custom_settings = {
        'DOWNLOAD_DELAY': 1,
        'RANDOMIZE_DOWNLOAD_DELAY': True,
        'AUTOTHROTTLE_ENABLED': True,
        'AUTOTHROTTLE_START_DELAY': 1,
        'AUTOTHROTTLE_MAX_DELAY': 5,
        'CONCURRENT_REQUESTS': 4,
    }

    def __init__(self, user_ids=None, start_time=None, end_time=None, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.user_ids = user_ids or ['1087770692']
        if isinstance(self.user_ids, str):
            self.user_ids = [self.user_ids]
        self.start_time = start_time  # YYYY-MM-DD
        self.end_time = end_time      # YYYY-MM-DD

    def start_requests(self):
        for user_id in self.user_ids:
            url = (
                f"https://weibo.com/ajax/statuses/searchProfile?"
                f"uid={user_id}&page=1&hasori=1&hastext=1&haspic=1"
                f"&hasvideo=1&hasmusic=1&hasret=1"
            )
            # 如果指定了时间范围，按天切片
            if self.start_time and self.end_time:
                start = datetime.datetime.strptime(self.start_time, '%Y-%m-%d')
                end = datetime.datetime.strptime(self.end_time, '%Y-%m-%d') + datetime.timedelta(days=1)
                current = start
                while current <= end:
                    chunk_end = min(current + datetime.timedelta(days=10), end)
                    chunk_url = url + f'&starttime={int(current.timestamp())}&endtime={int(chunk_end.timestamp())}'
                    headers = {'Referer': f'https://weibo.com/u/{user_id}'}
                    yield Request(chunk_url, callback=self.parse, headers=headers,
                                  meta={'user_id': user_id, 'page_num': 1})
                    current = chunk_end + datetime.timedelta(days=1)
            else:
                headers = {'Referer': f'https://weibo.com/u/{user_id}'}
                yield Request(url, callback=self.parse, headers=headers,
                              meta={'user_id': user_id, 'page_num': 1})

    def parse(self, response, **kwargs):
        data = json.loads(response.text)
        tweets = data.get('data', {}).get('list', [])
        user_id = response.meta['user_id']

        for tweet in tweets:
            item = parse_tweet_info(tweet)
            item['user_id'] = user_id
            if 'user' in item:
                item['screen_name'] = item['user'].get('nick_name', '')
                del item['user']
            else:
                item['screen_name'] = ''
            if item['isLongText']:
                url = "https://weibo.com/ajax/statuses/longtext?id=" + item['mblogid']
                yield Request(url, callback=parse_long_tweet,
                              headers={'Referer': f'https://weibo.com/u/{user_id}'},
                              meta={'item': item})
            else:
                yield item

        if tweets:
            page_num = response.meta['page_num']
            url = response.url.replace(f'page={page_num}', f'page={page_num + 1}')
            yield Request(url, callback=self.parse,
                          headers={'Referer': f'https://weibo.com/u/{user_id}'},
                          meta={'user_id': user_id, 'page_num': page_num + 1})
