#!/usr/bin/env python
# encoding: utf-8
"""
微博评论数据采集（改造版）
"""
import json
from scrapy import Spider
from scrapy.http import Request
from spiders.common import parse_user_info, parse_time, url_to_mid


class CommentSpider(Spider):
    name = "comment"

    custom_settings = {
        'DOWNLOAD_DELAY': 0.5,
        'RANDOMIZE_DOWNLOAD_DELAY': True,
        'AUTOTHROTTLE_ENABLED': False,
        'CONCURRENT_REQUESTS': 8,
    }

    def __init__(self, tweet_ids=None, flow='0', *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.tweet_ids = tweet_ids or ['Mb15BDYR0']
        if isinstance(self.tweet_ids, str):
            self.tweet_ids = self.tweet_ids.split(',')
        self.flow = flow  # 0=热度排序, 1=时间排序

    def start_requests(self):
        for tweet_id in self.tweet_ids:
            mid = url_to_mid(tweet_id)
            base_url = (
                f"https://weibo.com/ajax/statuses/buildComments?"
                f"flow={self.flow}&is_reload=1&id={mid}&is_show_bulletin=2&is_mix=0&count=50"
            )
            yield Request(base_url, callback=self.parse, headers={'Referer': 'https://weibo.com/'}, meta={
                'base_url': base_url, 'tweet_id': str(mid), 'sort_offset': 0
            })

    def parse(self, response, **kwargs):
        data = json.loads(response.text)
        tweet_id = response.meta['tweet_id']
        sort_offset = response.meta.get('sort_offset', 0)

        for i, comment_info in enumerate(data.get('data', [])):
            item = self.parse_comment(comment_info)
            item['tweet_id'] = tweet_id
            item['sort_order'] = sort_offset + i
            yield item

        # Paginate to next page of comments
        count = len(data.get('data', []))
        if data.get('max_id', 0) != 0 and count > 0:
            url = response.meta['base_url'] + '&max_id=' + str(data['max_id'])
            yield Request(url, callback=self.parse,
                          headers={'Referer': 'https://weibo.com/',
                                   'X-Requested-With': 'XMLHttpRequest'},
                          meta={'base_url': response.meta['base_url'],
                                'tweet_id': tweet_id,
                                'sort_offset': sort_offset + count})

    @staticmethod
    def parse_comment(data):
        item = dict()
        item['created_at'] = parse_time(data['created_at'])
        item['_id'] = data['id']
        item['like_counts'] = data['like_counts']
        item['ip_location'] = data.get('source', '')
        item['content'] = data['text_raw']
        item['comment_user'] = parse_user_info(data['user'])
        if 'reply_comment' in data:
            item['reply_comment'] = {
                '_id': data['reply_comment']['id'],
                'text': data['reply_comment']['text'],
                'user': parse_user_info(data['reply_comment']['user']),
            }
        return item
