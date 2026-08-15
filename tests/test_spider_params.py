import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))


class TestTweetSpiderParams:
    def test_stop_after_id_parsed(self):
        from spiders.tweet_by_user_id import TweetSpiderByUserID
        spider = TweetSpiderByUserID(user_ids='123', stop_after_id='999')
        assert spider.stop_after_id == '999'

    def test_stop_after_id_defaults_none(self):
        from spiders.tweet_by_user_id import TweetSpiderByUserID
        spider = TweetSpiderByUserID(user_ids='123')
        assert spider.stop_after_id is None

    def test_parse_stops_pagination_at_stop_after_id(self):
        """parse() should yield the matching tweet, skip later tweets, and not yield next-page Request."""
        from spiders.tweet_by_user_id import TweetSpiderByUserID
        from unittest.mock import MagicMock
        import json

        def _make_tweet(mid):
            return {
                'mid': mid,
                'mblogid': f'Mb{mid}',
                'created_at': 'Wed Oct 19 23:44:36 +0800 2022',
                'reposts_count': 0,
                'comments_count': 0,
                'attitudes_count': 0,
                'source': 'web',
                'text_raw': f'tweet {mid}',
                'pic_num': 0,
                'user': {
                    'id': 123,
                    'avatar_hd': 'http://example.com/avatar.jpg',
                    'screen_name': 'user1',
                    'verified': False,
                },
            }

        # tweet 1 (mid=111) is the stop_after_id, tweet 2 (mid=222) should be skipped
        tweets_data = [_make_tweet(111), _make_tweet(222)]
        response = MagicMock()
        response.text = json.dumps({'ok': 1, 'data': {'list': tweets_data}})
        response.meta = {'page_num': 1, 'user_id': '123'}
        response.url = 'https://weibo.com/ajax/statuses/searchProfile?uid=123&page=1'

        spider = TweetSpiderByUserID(user_ids='123', stop_after_id='111')
        results = list(spider.parse(response))

        # Should yield exactly 1 item (the matching tweet), no next-page Request
        assert len(results) == 1
        assert results[0]['_id'] == '111'

    def _empty_response(self, page_num, chunk_total, collected, retries=0, total='346'):
        from unittest.mock import MagicMock
        import json
        response = MagicMock()
        response.text = json.dumps({'ok': 1, 'data': {'list': [], 'total': total}})
        response.url = f'https://weibo.com/ajax/statuses/searchProfile?uid=123&page={page_num}'
        response.meta = {
            'page_num': page_num, 'user_id': '123',
            'chunk_total': chunk_total, 'collected': collected, 'empty_retries': retries,
        }
        return response

    def test_empty_page_retries_when_collected_lt_total(self):
        """空页但收集数<窗口总数 → 重试同一页（防瞬时失败丢切片）"""
        from spiders.tweet_by_user_id import TweetSpiderByUserID
        spider = TweetSpiderByUserID(user_ids='123')
        results = list(spider.parse(self._empty_response(page_num=2, chunk_total=346, collected=100)))
        requests = [r for r in results if hasattr(r, 'url')]
        assert len(requests) == 1
        assert 'page=2' in requests[0].url  # 重试的是同一页
        assert requests[0].meta['empty_retries'] == 1

    def test_empty_page_stops_when_collected_reaches_total(self):
        """空页且收集数已达窗口总数 → 正常结束，不重试"""
        from spiders.tweet_by_user_id import TweetSpiderByUserID
        spider = TweetSpiderByUserID(user_ids='123')
        results = list(spider.parse(self._empty_response(page_num=4, chunk_total=346, collected=346)))
        assert results == []

    def test_empty_page_gives_up_after_max_retries(self):
        """重试次数用尽且仍没收齐 → 放弃并停止（日志记录数据空洞）"""
        from spiders.tweet_by_user_id import TweetSpiderByUserID
        spider = TweetSpiderByUserID(user_ids='123')
        results = list(spider.parse(self._empty_response(page_num=2, chunk_total=346, collected=100, retries=2)))
        assert results == []


class TestCommentSpiderParams:
    def test_max_pages_parsed(self):
        from spiders.comment import CommentSpider
        spider = CommentSpider(tweet_ids='Mb1', max_pages='2')
        assert spider.max_pages == 2

    def test_max_pages_defaults_none(self):
        from spiders.comment import CommentSpider
        spider = CommentSpider(tweet_ids='Mb1')
        assert spider.max_pages is None

    def test_parse_stops_at_max_pages(self):
        """parse() should not yield next-page Request when max_pages is reached."""
        from spiders.comment import CommentSpider
        from unittest.mock import MagicMock
        import json

        comments_data = [
            {'id': 1, 'idstr': '1', 'text_raw': 'comment 1',
             'user': {'id': 10, 'idstr': '10', 'screen_name': 'user',
                      'avatar_hd': 'http://example.com/a.jpg', 'verified': False},
             'created_at': 'Mon Jan 01 10:00:00 +0800 2024',
             'like_counts': 0, 'source': ''},
        ]
        response = MagicMock()
        response.text = json.dumps({
            'ok': 1,
            'data': comments_data,
            'max_id': 999,  # would normally trigger pagination
        })
        response.meta = {
            'base_url': 'https://weibo.com/ajax/statuses/buildComments?flow=0&id=123',
            'tweet_id': '123',
            'tweet_index': 1,
            'tweet_total': 1,
            'sort_offset': 0,
            'comment_count': 0,
            'top_count': 0,
            'page_num': 1,
        }

        spider = CommentSpider(tweet_ids='Mb1', max_pages='1')
        results = list(spider.parse(response))

        # Should yield the comment item but NOT a next-page Request
        items = [r for r in results if not hasattr(r, 'url')]
        requests = [r for r in results if hasattr(r, 'url')]
        assert len(items) == 1
        assert len(requests) == 0  # max_pages=1, so no pagination
