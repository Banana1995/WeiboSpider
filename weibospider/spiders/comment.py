#!/usr/bin/env python
# encoding: utf-8
"""
微博评论数据采集（改造版）
"""
import json
from scrapy import Spider, signals
from scrapy.http import Request
from spiders.common import parse_user_info, parse_time, url_to_mid


class CommentSpider(Spider):
    name = "comment"

    custom_settings = {
        'DOWNLOAD_DELAY': 0.5,
        'RANDOMIZE_DOWNLOAD_DELAY': True,
        'AUTOTHROTTLE_ENABLED': False,
        'CONCURRENT_REQUESTS': 8,
        # Scrapy 2.5 otherwise leaves the JSON feed empty when there are no items.
        'FEED_STORE_EMPTY': True,
    }

    def __init__(self, tweet_ids=None, flow='0', max_pages=None, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.tweet_ids = tweet_ids or ['Mb15BDYR0']
        if isinstance(self.tweet_ids, str):
            self.tweet_ids = self.tweet_ids.split(',')
        self.flow = flow  # 0=热度排序, 1=时间排序
        self.max_pages = int(max_pages) if max_pages else None  # limit pages per tweet
        self._completed_tweets = set()
        self._crawl_errors = set()
        self._item_count = 0

    @classmethod
    def from_crawler(cls, crawler, *args, **kwargs):
        spider = super().from_crawler(crawler, *args, **kwargs)
        crawler.signals.connect(spider._processing_error, signal=signals.spider_error)
        crawler.signals.connect(spider._processing_error, signal=signals.item_error)
        return spider

    def _processing_error(self, **kwargs):
        self._crawl_errors.add('processing_error')

    def request_failed(self, failure):
        self._crawl_errors.add('request_failed')
        self.logger.error('Comment request failed; no empty-result success will be reported')

    def closed(self, reason):
        # Scrapy can exit with code 0 after HTTP, callback, or pipeline failures.
        # Report an explicit outcome so the caller can distinguish those from zero comments.
        if reason != 'finished' or len(self._completed_tweets) != len(set(self.tweet_ids)):
            self._crawl_errors.add('incomplete')
        error = ('cookie_expired' if 'cookie_expired' in self._crawl_errors
                 else next(iter(sorted(self._crawl_errors - {'incomplete'})), None))
        if error is None and self._crawl_errors:
            error = 'incomplete'
        self.logger.info('COMMENT_CRAWL_RESULT %s', json.dumps({
            'status': 'failed' if error else 'completed',
            'error': error,
            'item_count': self._item_count,
        }))

    def start_requests(self):
        self.logger.info("Starting comments crawl for %d tweets", len(self.tweet_ids))
        for idx, tweet_id in enumerate(self.tweet_ids):
            mid = url_to_mid(tweet_id)
            base_url = (
                f"https://weibo.com/ajax/statuses/buildComments?"
                f"flow={self.flow}&is_reload=1&id={mid}&is_show_bulletin=2&is_mix=1&count=50"
            )
            yield Request(base_url, callback=self.parse, errback=self.request_failed,
                          headers={'Referer': 'https://weibo.com/'}, meta={
                'base_url': base_url, 'tweet_id': str(mid),
                'tweet_index': idx + 1, 'tweet_total': len(self.tweet_ids),
                'sort_offset': 0, 'comment_count': 0, 'top_count': 0,
                'page_num': 1
            })

    def parse(self, response, **kwargs):
        tweet_id = response.meta['tweet_id']
        sort_offset = response.meta.get('sort_offset', 0)
        tweet_idx = response.meta.get('tweet_index', 0)
        tweet_total = response.meta.get('tweet_total', 0)

        try:
            data = json.loads(response.text)
        except json.JSONDecodeError:
            self._crawl_errors.add('non_json')
            self.logger.error("[%d/%d] tweet %s: non-JSON response",
                              tweet_idx, tweet_total, tweet_id)
            return

        if not isinstance(data, dict):
            self._crawl_errors.add('invalid_response')
            return
        if data.get('ok') == -100:
            self._crawl_errors.add('cookie_expired')
            self.logger.critical(
                "Weibo API returned ok=-100 (not logged in / cookie expired). "
                "Please update your cookie in the web UI config."
            )
            return
        if data.get('ok') != 1:
            self._crawl_errors.add('api_error')
            self.logger.warning("[%d/%d] tweet %s: API rejected comments request",
                                tweet_idx, tweet_total, tweet_id)
            return

        comment_count = response.meta.get('comment_count', 0)
        top_count = response.meta.get('top_count', 0)
        MAX_TOP = 100
        MAX_SUB = 10

        comments = data.get('data')
        if not isinstance(comments, list):
            self._crawl_errors.add('invalid_response')
            return
        seq = sort_offset
        sub_count = 0
        actual_top_yielded = 0
        for comment_info in comments:
            if top_count >= MAX_TOP:
                break
            if 'data' in comment_info and 'id' not in comment_info:
                comment_info = comment_info['data']
            item = self.parse_comment(comment_info)
            item['tweet_id'] = tweet_id
            item['sort_order'] = seq
            seq += 1
            top_count += 1
            actual_top_yielded += 1
            self._item_count += 1
            yield item

            subs = comment_info.get('comments', [])
            if isinstance(subs, list):
                for si, sub in enumerate(subs):
                    if not isinstance(sub, dict) or si >= MAX_SUB:
                        break
                    s_item = self.parse_comment(sub)
                    s_item['tweet_id'] = tweet_id
                    s_item['sort_order'] = seq
                    seq += 1
                    if 'parent_comment_id' not in s_item:
                        s_item['parent_comment_id'] = str(comment_info.get('id', ''))
                    self._item_count += 1
                    yield s_item
                    sub_count += 1

        new_count = actual_top_yielded + sub_count
        total_count = comment_count + new_count
        is_first_page = sort_offset == 0
        reached_limit = top_count >= MAX_TOP

        if is_first_page:
            p1_msg = f"page 1 got {new_count} comments"
            if sub_count:
                p1_msg += f" ({actual_top_yielded} top + {sub_count} sub)"
            self.logger.info("[%d/%d] tweet %s: %s",
                             tweet_idx, tweet_total, tweet_id, p1_msg)

        # Paginate to next page of comments (stop if limit reached)
        current_page = response.meta.get('page_num', 1)
        reached_max_pages = self.max_pages is not None and current_page >= self.max_pages
        has_next = data.get('max_id', 0) != 0 and len(comments) > 0
        if not reached_limit and not reached_max_pages and has_next:
            url = response.meta['base_url'] + '&max_id=' + str(data['max_id'])
            yield Request(url, callback=self.parse, errback=self.request_failed,
                          headers={'Referer': 'https://weibo.com/',
                                   'X-Requested-With': 'XMLHttpRequest'},
                          meta={'base_url': response.meta['base_url'],
                                'tweet_id': tweet_id,
                                'tweet_index': tweet_idx,
                                'tweet_total': tweet_total,
                                'sort_offset': seq,
                                'comment_count': total_count,
                                'top_count': top_count,
                                'page_num': current_page + 1})
        else:
            self._completed_tweets.add(tweet_id)
            stop_reason = 'top_limit' if reached_limit else 'exhausted'
            if has_next and reached_max_pages and not reached_limit:
                stop_reason = 'page_limit'
                self._crawl_errors.add('page_limit')
            self.logger.info(
                "[%d/%d] tweet %s: %d comments total (%d top + %d sub), pages=%d, stop=%s",
                tweet_idx, tweet_total, tweet_id, total_count,
                top_count, total_count - top_count, current_page, stop_reason)

    @staticmethod
    def parse_comment(data):
        item = dict()
        item['created_at'] = parse_time(data['created_at']) if 'created_at' in data else ''
        item['_id'] = data.get('id', '')
        item['like_counts'] = data.get('like_counts', 0)
        item['ip_location'] = data.get('source', '')
        item['content'] = data.get('text_raw') or data.get('text', '')
        item['comment_user'] = parse_user_info(data['user']) if 'user' in data else {}
        comment_id = str(data.get('id', ''))
        rootidstr = data.get('rootidstr', '')
        if rootidstr and rootidstr != '0' and rootidstr != comment_id:
            item['parent_comment_id'] = rootidstr
        if 'reply_comment' in data and data['reply_comment']:
            item['reply_comment'] = {
                '_id': data['reply_comment']['id'],
                'text': data['reply_comment'].get('text', ''),
                'user': parse_user_info(data['reply_comment']['user']),
            }
        return item
