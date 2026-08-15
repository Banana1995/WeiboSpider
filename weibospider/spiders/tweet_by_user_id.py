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

# 空页/异常页重试次数：防瞬时失败（限流、网络抖动）导致整个时间切片丢失
PAGE_RETRY_TIMES = 2


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

    def __init__(self, user_ids=None, start_time=None, end_time=None, stop_after_id=None, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.user_ids = user_ids or ['1087770692']
        if isinstance(self.user_ids, str):
            self.user_ids = [self.user_ids]
        self.start_time = start_time  # YYYY-MM-DD
        self.end_time = end_time      # YYYY-MM-DD
        self.stop_after_id = stop_after_id  # stop pagination when this tweet id is seen

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
                                  meta={'user_id': user_id, 'page_num': 1,
                                        'chunk_total': None, 'collected': 0, 'empty_retries': 0})
                    current = chunk_end + datetime.timedelta(days=1)
            else:
                headers = {'Referer': f'https://weibo.com/u/{user_id}'}
                yield Request(url, callback=self.parse, headers=headers,
                              meta={'user_id': user_id, 'page_num': 1,
                                    'chunk_total': None, 'collected': 0, 'empty_retries': 0})

    def _reissue(self, response, retries):
        """Re-request the same page with an incremented retry counter."""
        user_id = response.meta.get('user_id', '')
        return Request(response.url, callback=self.parse,
                       headers={'Referer': f'https://weibo.com/u/{user_id}'},
                       meta={**response.meta, 'empty_retries': retries + 1},
                       dont_filter=True)

    def parse(self, response, **kwargs):
        page_num = response.meta['page_num']
        user_id = response.meta['user_id']
        chunk_total = response.meta.get('chunk_total')
        collected = response.meta.get('collected', 0)
        retries = response.meta.get('empty_retries', 0)

        try:
            data = json.loads(response.text)
        except Exception:
            data = None

        if data is not None and data.get('ok') == -100:
            self.logger.critical(
                "Weibo API returned ok=-100 (not logged in / cookie expired). "
                "Please update your cookie in the web UI config."
            )
            return

        if data is None or data.get('ok') != 1:
            if retries < PAGE_RETRY_TIMES:
                self.logger.warning("page=%d invalid response, retrying (%d/%d)",
                                    page_num, retries + 1, PAGE_RETRY_TIMES)
                yield self._reissue(response, retries)
            else:
                self.logger.error("page=%d invalid response after %d retries, aborting this chunk",
                                  page_num, PAGE_RETRY_TIMES)
            return

        d = data.get('data') or {}
        tweets = d.get('list', [])
        if chunk_total is None:
            try:
                chunk_total = int(d.get('total') or 0)
            except (TypeError, ValueError):
                chunk_total = 0
            response.meta['chunk_total'] = chunk_total

        if page_num == 1 or page_num % 5 == 0:
            self.logger.info("page=%d got %d tweets", page_num, len(tweets))

        if not tweets:
            # 空页：已知窗口总数却没收齐 → 瞬时失败，重试；否则为正常翻页结束
            if chunk_total and collected < chunk_total and retries < PAGE_RETRY_TIMES:
                self.logger.warning("page=%d empty but collected=%d < total=%s, retrying (%d/%d)",
                                    page_num, collected, chunk_total, retries + 1, PAGE_RETRY_TIMES)
                yield self._reissue(response, retries)
            elif chunk_total and collected < chunk_total:
                self.logger.warning("page=%d empty after retries: collected=%d < total=%s (可能的数据空洞)",
                                    page_num, collected, chunk_total)
            return

        stop_pagination = False
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
            collected += 1

            if self.stop_after_id and str(item.get('_id')) == str(self.stop_after_id):
                self.logger.info("stop_after_id=%s reached, stopping pagination", self.stop_after_id)
                stop_pagination = True
                break

        if tweets and not stop_pagination:
            url = response.url.replace(f'page={page_num}', f'page={page_num + 1}')
            yield Request(url, callback=self.parse,
                          headers={'Referer': f'https://weibo.com/u/{user_id}'},
                          meta={'user_id': user_id, 'page_num': page_num + 1,
                                'chunk_total': chunk_total, 'collected': collected,
                                'empty_retries': 0})
