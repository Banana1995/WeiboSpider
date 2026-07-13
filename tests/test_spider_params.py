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
