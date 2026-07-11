import os
import sys
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))

from spiders.common import parse_tweet_info, parse_user_info, parse_time, url_to_mid


def _make_tweet_data(mid='1234567890', mblogid='MbTest', content='hello world',
                     user=None, retweeted_status=None, is_long_text=False,
                     pic_ids=None, source='<a href="x">iPhone</a>', **overrides):
    """Helper to build Weibo API tweet data dict."""
    if user is None:
        user = {
            'id': 1087770692, 'screen_name': 'test_user', 'avatar_hd': '',
            'verified': False,
        }
    data = {
        'mid': mid, 'mblogid': mblogid,
        'created_at': 'Wed Oct 19 12:00:00 +0800 2022',
        'reposts_count': 5, 'comments_count': 3, 'attitudes_count': 10,
        'source': source, 'text_raw': content,
        'pic_num': len(pic_ids) if pic_ids else 0,
        'pic_ids': pic_ids or [],
        'user': user,
        'isLongText': is_long_text,
        'retweeted_status': retweeted_status,
    }
    if 'continue_tag' in overrides:
        data['continue_tag'] = overrides.pop('continue_tag')
    data.update(overrides)
    return data


class TestParseTweetInfo:
    def test_basic_tweet(self):
        data = _make_tweet_data()
        result = parse_tweet_info(data)
        assert result['_id'] == '1234567890'
        assert result['mblogid'] == 'MbTest'
        assert result['content'] == 'hello world'
        assert result['reposts_count'] == 5
        assert result['comments_count'] == 3
        assert result['attitudes_count'] == 10
        assert result['is_retweet'] is False
        assert result['isLongText'] is False
        assert result['user']['nick_name'] == 'test_user'

    def test_source_strips_html(self):
        data = _make_tweet_data(source='<a href="x" rel="nofollow">iPhone 15 Pro</a>')
        result = parse_tweet_info(data)
        assert result['source'] == 'iPhone 15 Pro'

    def test_source_no_html(self):
        data = _make_tweet_data(source='iPhone')
        result = parse_tweet_info(data)
        assert result['source'] == 'iPhone'

    def test_pic_urls(self):
        data = _make_tweet_data(pic_ids=['abc123', 'def456'])
        result = parse_tweet_info(data)
        assert result['pic_num'] == 2
        assert result['pic_urls'] == [
            'https://wx1.sinaimg.cn/orj960/abc123',
            'https://wx1.sinaimg.cn/orj960/def456',
        ]

    def test_no_pics(self):
        data = _make_tweet_data(pic_ids=[])
        result = parse_tweet_info(data)
        assert result['pic_num'] == 0
        assert result['pic_urls'] == []

    def test_long_text_tag(self):
        data = _make_tweet_data(is_long_text=True, continue_tag=True)
        result = parse_tweet_info(data)
        assert result['isLongText'] is True

    def test_long_text_no_continue_tag(self):
        data = _make_tweet_data(is_long_text=True)
        result = parse_tweet_info(data)
        assert result['isLongText'] is False

    def test_with_reads_count(self):
        data = _make_tweet_data(reads_count=999)
        result = parse_tweet_info(data)
        assert result['reads_count'] == 999

    def test_without_reads_count(self):
        data = _make_tweet_data()
        result = parse_tweet_info(data)
        assert 'reads_count' not in result

    def test_geo_ip_location(self):
        data = _make_tweet_data(geo='earth', region_name='北京')
        result = parse_tweet_info(data)
        assert result['geo'] == 'earth'
        assert result['ip_location'] == '北京'

    def test_no_geo_no_ip(self):
        data = _make_tweet_data()
        result = parse_tweet_info(data)
        assert result['geo'] is None
        assert result['ip_location'] is None

    def test_url_format(self):
        data = _make_tweet_data(mid='1234567890', mblogid='MbTest')
        result = parse_tweet_info(data)
        assert result['url'] == 'https://weibo.com/1087770692/MbTest'

    def test_retweet_basic(self):
        retweet = {
            'mid': '9876543210', 'text_raw': 'original content',
            'user': {'id': 999, 'screen_name': '原博主'},
            'pic_ids': ['r1', 'r2'],
        }
        data = _make_tweet_data(retweeted_status=retweet)
        result = parse_tweet_info(data)
        assert result['is_retweet'] is True
        assert result['retweet_id'] == '9876543210'
        assert result['retweet_content'] == 'original content'
        assert result['retweet_user'] == '原博主'
        assert result['retweet_pic_urls'] == [
            'https://wx1.sinaimg.cn/orj960/r1',
            'https://wx1.sinaimg.cn/orj960/r2',
        ]
        assert 'retweet_has_video' not in result

    def test_retweeted_status_is_none(self):
        """retweeted_status key exists but value is None (deleted source tweet)."""
        data = _make_tweet_data(retweeted_status=None)
        result = parse_tweet_info(data)
        assert result['is_retweet'] is False
        assert 'retweet_id' not in result

    def test_retweet_user_is_none(self):
        """retweeted_status.user is None (deleted user)."""
        retweet = {
            'mid': '9876543210', 'text_raw': 'original content',
            'user': None,
            'pic_ids': [],
        }
        data = _make_tweet_data(retweeted_status=retweet)
        result = parse_tweet_info(data)
        assert result['is_retweet'] is True
        assert result['retweet_user'] == ''

    def test_retweet_text_raw_is_none(self):
        """retweeted_status.text_raw is None."""
        retweet = {
            'mid': '9876543210', 'text_raw': None,
            'user': {'id': 999, 'screen_name': '原博主'},
            'pic_ids': [],
        }
        data = _make_tweet_data(retweeted_status=retweet)
        result = parse_tweet_info(data)
        assert result['retweet_content'] == ''

    def test_retweet_no_pic_ids(self):
        retweet = {
            'mid': '9876543210', 'text_raw': 'content',
            'user': {'id': 999, 'screen_name': '原博主'},
        }
        data = _make_tweet_data(retweeted_status=retweet)
        result = parse_tweet_info(data)
        assert result['retweet_pic_urls'] == []

    def test_retweet_with_video(self):
        retweet = {
            'mid': '9876543210', 'text_raw': 'video post',
            'user': {'id': 999, 'screen_name': '视频博主'},
            'pic_ids': [],
            'page_info': {'object_type': 'video'},
        }
        data = _make_tweet_data(retweeted_status=retweet)
        result = parse_tweet_info(data)
        assert result['retweet_has_video'] is True

    def test_retweet_page_info_is_none(self):
        """retweeted_status.page_info is None."""
        retweet = {
            'mid': '9876543210', 'text_raw': 'content',
            'user': {'id': 999, 'screen_name': '博主'},
            'pic_ids': [],
            'page_info': None,
        }
        data = _make_tweet_data(retweeted_status=retweet)
        result = parse_tweet_info(data)
        assert 'retweet_has_video' not in result

    def test_video_tweet(self):
        data = _make_tweet_data(
            page_info={
                'object_type': 'video',
                'media_info': {'stream_url': 'https://video.example.com/1.mp4'},
            }
        )
        result = parse_tweet_info(data)
        assert result['video'] == 'https://video.example.com/1.mp4'

    def test_content_strips_zero_width_space(self):
        data = _make_tweet_data(content='hello\u200b world\u200b')
        result = parse_tweet_info(data)
        assert result['content'] == 'hello world'


class TestParseUserInfo:
    def test_basic_user(self):
        data = {'id': 123, 'avatar_hd': '', 'screen_name': 'test', 'verified': False}
        result = parse_user_info(data)
        assert result['_id'] == '123'
        assert result['nick_name'] == 'test'
        assert result['verified'] is False

    def test_verified_user(self):
        data = {
            'id': 456, 'avatar_hd': '', 'screen_name': 'v_user',
            'verified': True, 'verified_type': 1, 'verified_reason': '微博认证',
        }
        result = parse_user_info(data)
        assert result['verified'] is True
        assert result['verified_type'] == 1
        assert result['verified_reason'] == '微博认证'


class TestParseTime:
    def test_standard_format(self):
        result = parse_time('Wed Oct 19 12:00:00 +0800 2022')
        assert result == '2022-10-19 12:00:00'


class TestUrlToMid:
    def test_basic(self):
        result = url_to_mid('z0JH2lOMb')
        assert result == 3501756485200075

    def test_long_mblogid(self):
        result = url_to_mid('Mb15BDYR0')
        assert isinstance(result, int)
        assert result > 0


def _make_comment_data(id='1001', created_at='Wed Oct 19 12:00:00 +0800 2022',
                        text_raw='hello', user=None, like_counts=5, source='北京',
                        rootidstr=None, reply_comment=None, **overrides):
    if user is None:
        user = {'id': 123, 'screen_name': 'test', 'avatar_hd': '', 'verified': False}
    data = {
        'id': id, 'created_at': created_at, 'text_raw': text_raw,
        'user': user, 'like_counts': like_counts, 'source': source,
    }
    if rootidstr:
        data['rootidstr'] = rootidstr
    if reply_comment:
        data['reply_comment'] = reply_comment
    data.update(overrides)
    return data


class TestParseComment:
    def test_basic(self):
        from spiders.comment import CommentSpider
        data = _make_comment_data()
        item = CommentSpider.parse_comment(data)
        assert item['_id'] == '1001'
        assert item['content'] == 'hello'
        assert item['like_counts'] == 5
        assert item['ip_location'] == '北京'
        assert 'parent_comment_id' not in item

    def test_missing_created_at(self):
        from spiders.comment import CommentSpider
        data = _make_comment_data(created_at=None)
        del data['created_at']
        item = CommentSpider.parse_comment(data)
        assert item['created_at'] == ''

    def test_missing_user(self):
        from spiders.comment import CommentSpider
        data = _make_comment_data(user=None)
        del data['user']
        item = CommentSpider.parse_comment(data)
        assert item['comment_user'] == {}

    def test_missing_text_raw(self):
        from spiders.comment import CommentSpider
        data = _make_comment_data(text_raw=None)
        del data['text_raw']
        item = CommentSpider.parse_comment(data)
        assert item['content'] == ''

    def test_sub_comment_with_rootidstr(self):
        from spiders.comment import CommentSpider
        data = _make_comment_data(rootidstr='999')
        item = CommentSpider.parse_comment(data)
        assert item['parent_comment_id'] == '999'

    def test_not_sub_comment_rootidstr_zero(self):
        from spiders.comment import CommentSpider
        data = _make_comment_data(rootidstr='0')
        item = CommentSpider.parse_comment(data)
        assert 'parent_comment_id' not in item

    def test_with_reply_comment(self):
        from spiders.comment import CommentSpider
        reply = {'id': '2001', 'text': 'reply text', 'user': {'id': 456, 'screen_name': 'replier', 'avatar_hd': '', 'verified': False}}
        data = _make_comment_data(reply_comment=reply)
        item = CommentSpider.parse_comment(data)
        assert item['reply_comment']['_id'] == '2001'
        assert item['reply_comment']['text'] == 'reply text'

    def test_missing_like_counts(self):
        from spiders.comment import CommentSpider
        data = {'id': '1001', 'text_raw': 'hello', 'created_at': 'Wed Oct 19 12:00:00 +0800 2022',
                'user': {'id': 123, 'screen_name': 'test', 'avatar_hd': '', 'verified': False}}
        item = CommentSpider.parse_comment(data)
        assert item['like_counts'] == 0
