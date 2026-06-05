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

    def __init__(self, tweet_ids=None, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.tweet_ids = tweet_ids or ['Mb15BDYR0']
        if isinstance(self.tweet_ids, str):
            self.tweet_ids = self.tweet_ids.split(',')

    def start_requests(self):
        for tweet_id in self.tweet_ids:
            mid = url_to_mid(tweet_id)
            url = (
                f"https://weibo.com/ajax/statuses/buildComments?"
                f"is_reload=1&id={mid}&is_show_bulletin=2&is_mix=0&count=20"
            )
            yield Request(url, callback=self.parse, meta={
                'source_url': url, 'tweet_id': str(mid)
            })

    def parse(self, response, **kwargs):
        data = json.loads(response.text)
        tweet_id = response.meta['tweet_id']

        for comment_info in data.get('data', []):
            item = self.parse_comment(comment_info)
            item['tweet_id'] = tweet_id
            yield item
            if 'more_info' in comment_info:
                url = (
                    f"https://weibo.com/ajax/statuses/buildComments?"
                    f"is_reload=1&id={comment_info['id']}"
                    f"&is_show_bulletin=2&is_mix=1&fetch_level=1&max_id=0&count=100"
                )
                yield Request(url, callback=self.parse, priority=20,
                              meta={'tweet_id': tweet_id})

        if data.get('max_id', 0) != 0 and 'fetch_level=1' not in response.url:
            url = response.meta['source_url'] + '&max_id=' + str(data['max_id'])
            yield Request(url, callback=self.parse, meta={
                'source_url': response.meta['source_url'],
                'tweet_id': tweet_id,
            })

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
