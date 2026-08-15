# tests/test_xueqiu_comments.py
"""Tests for xueqiu comment conversion and crawl helpers.

Pure-function tests that import app functions directly (no Flask test client,
which is broken in this venv due to werkzeug __version__ env issue).
"""
import os
import sys
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))

import app as app_module


@pytest.fixture(autouse=True)
def _app_import():
    """Ensure app module is imported once (functions are pure)."""
    assert app_module._xueqiu_clean_html
    yield


class TestXueqiuCommentToItem:
    def test_maps_basic_fields(self):
        cm = {
            'id': 419916424,
            'user_id': 1054212627,
            'created_at': 1786757651000,
            'text': '这是<b>评论</b>正文',
            'like_count': 7,
            'ip_location': '上海',
            'user': {'id': 1054212627, 'screen_name': '鹿公'},
        }
        item = app_module._xueqiu_comment_to_item(cm, 'xq404947164')
        assert item['_id'] == 'xc419916424'
        assert item['tweet_id'] == 'xq404947164'
        assert item['content'] == '这是评论正文'
        assert item['like_counts'] == 7
        assert item['ip_location'] == '上海'
        assert item['comment_user']['nick_name'] == '鹿公'
        assert item['comment_user']['_id'] == '1054212627'
        assert item['parent_comment_id'] is None
        assert item['platform'] == 'xueqiu'

    def test_clean_html_strips_tags(self):
        cm = {
            'id': 1, 'user_id': 1, 'created_at': 1786757651000,
            'text': '氧化铝也需要运费的<img src="//x.png" title="[笑哭]">原料和成品 都有运输需求',
            'like_count': 0, 'ip_location': '', 'user': {'screen_name': 'A'},
        }
        item = app_module._xueqiu_comment_to_item(cm, 'xq1')
        assert item['content'] == '氧化铝也需要运费的原料和成品 都有运输需求'

    def test_maps_reply_and_parent(self):
        cm = {
            'id': 419819881,
            'user_id': 6323722766,
            'created_at': 1786675625000,
            'text': '回复@要一个小目标: 说得对',
            'like_count': 1,
            'ip_location': '',
            'in_reply_to_comment_id': 419775280,
            'reply_screenName': '要一个小目标',
            'user': {'screen_name': 'gkimyvkhn'},
        }
        item = app_module._xueqiu_comment_to_item(cm, 'xq404947164')
        assert item['parent_comment_id'] == 'xc419775280'
        assert item['reply_comment']['user']['nick_name'] == '要一个小目标'
        assert item['reply_comment']['_id'] == '419775280'

    def test_missing_created_at(self):
        cm = {'id': 9, 'user_id': 1, 'text': '无时间',
              'like_count': 0, 'ip_location': '', 'user': {}}
        item = app_module._xueqiu_comment_to_item(cm, 'xq9')
        assert item['created_at'] is None


class TestFlattenXueqiuComments:
    def test_flattens_child_comments_under_parent(self):
        parent = {
            'id': 100, 'user_id': 1, 'created_at': 1786757651000,
            'text': '顶层评论', 'like_count': 3, 'ip_location': '北京',
            'user': {'screen_name': 'P'}, 'child_comments': [
                {'id': 101, 'user_id': 2, 'created_at': 1786757652000,
                 'text': '回复1', 'like_count': 0, 'ip_location': '',
                 'user': {'screen_name': 'C1'}},
                {'id': 102, 'user_id': 3, 'created_at': 1786757653000,
                 'text': '回复2', 'like_count': 0, 'ip_location': '',
                 'user': {'screen_name': 'C2'}},
            ],
        }
        items = app_module._flatten_xueqiu_comments([parent], 'xq1')
        assert [i['_id'] for i in items] == ['xc100', 'xc101', 'xc102']
        assert items[1]['parent_comment_id'] == 'xc100'
        assert items[2]['parent_comment_id'] == 'xc100'
        assert items[0]['sort_order'] == 0
        assert items[1]['sort_order'] == 1
        assert items[2]['sort_order'] == 2

    def test_skips_non_dict_children(self):
        parent = {
            'id': 100, 'user_id': 1, 'created_at': 1786757651000,
            'text': '顶层', 'like_count': 0, 'ip_location': '',
            'user': {'screen_name': 'P'}, 'child_comments': ['bad', None],
        }
        items = app_module._flatten_xueqiu_comments([parent], 'xq1')
        assert [i['_id'] for i in items] == ['xc100']


class TestSelectTopXueqiuItems:
    def _cm(self, cid, likes, created=None, in_reply_to=None):
        return {
            'id': cid, 'user_id': 1, 'created_at': created or (1786757651000 + cid),
            'text': f'评论{cid}', 'like_count': likes, 'ip_location': '',
            'user': {'screen_name': f'U{cid}'},
            'in_reply_to_comment_id': in_reply_to,
            'reply_screenName': '某用户' if in_reply_to else None,
        }

    def test_caps_at_max_top_and_sorts_by_heat(self):
        comments = [self._cm(i, i) for i in range(1, 105)]  # 104 comments
        items = app_module._select_top_xueqiu_items(comments, 'xq1', max_top=100)
        # only 100 kept, hottest first
        assert len(items) == 100
        assert items[0]['_id'] == 'xc104'
        assert items[99]['_id'] == 'xc5'
        # sort_order is heat rank
        assert items[0]['sort_order'] == 0
        assert items[99]['sort_order'] == 99

    def test_keeps_children_of_selected_top(self):
        # Real API shape: replies are inline with in_reply_to_comment_id
        hot = self._cm(10, 99)
        child1 = self._cm(101, 0, in_reply_to=10)
        child2 = self._cm(102, 0, in_reply_to=10)
        cold = self._cm(11, 0)
        items = app_module._select_top_xueqiu_items([hot, cold, child1, child2], 'xq1', max_top=1)
        ids = [i['_id'] for i in items]
        assert ids == ['xc10', 'xc101', 'xc102']
        assert items[1]['parent_comment_id'] == 'xc10'
        assert items[2]['parent_comment_id'] == 'xc10'

    def test_all_kept_when_under_limit(self):
        comments = [self._cm(i, i) for i in range(1, 10)]
        items = app_module._select_top_xueqiu_items(comments, 'xq1', max_top=100)
        assert len(items) == 9
        assert items[0]['_id'] == 'xc9'

    def test_zero_like_comments_kept_until_cap(self):
        comments = [self._cm(1, 0), self._cm(2, 0), self._cm(3, 0)]
        items = app_module._select_top_xueqiu_items(comments, 'xq1', max_top=2)
        assert len(items) == 2


class TestSelectTopWithInlineReplies:
    """The xueqiu comments API returns a FLAT list mixing top-level comments
    and inline replies (in_reply_to_comment_id set). Replies can chain to
    other replies. Top-100 selection must rank ONLY top-level by heat and
    attach replies under their root top-level parent.
    """

    def _cm(self, cid, likes, in_reply_to=None, screen_name='U'):
        return {
            'id': cid, 'user_id': 1, 'created_at': 1786757651000,
            'text': f'评论{cid}', 'like_count': likes, 'ip_location': '',
            'user': {'screen_name': f'{screen_name}{cid}'},
            'in_reply_to_comment_id': in_reply_to,
            'reply_screenName': '某用户' if in_reply_to else None,
        }

    def test_ranks_only_top_level_by_heat(self):
        # top-level A (10 likes), top-level B (1 like), reply C (999 likes, belongs to A)
        comments = [
            self._cm(1, 10),
            self._cm(2, 1),
            self._cm(3, 999, in_reply_to=1),
        ]
        items = app_module._select_top_xueqiu_items(comments, 'xq1', max_top=10)
        ids = [i['_id'] for i in items]
        # top-level ranked by heat: xc1(10), xc2(1); reply xc3 attached under xc1
        assert ids == ['xc1', 'xc3', 'xc2']
        assert items[0]['parent_comment_id'] is None
        assert items[1]['parent_comment_id'] == 'xc1'
        assert items[2]['parent_comment_id'] is None

    def test_reply_chained_to_reply_resolves_to_root(self):
        # A top-level (id 10), reply R1->A (id 20), reply R2->R1 (id 30)
        comments = [
            self._cm(10, 5),
            self._cm(20, 0, in_reply_to=10),
            self._cm(30, 0, in_reply_to=20),
        ]
        items = app_module._select_top_xueqiu_items(comments, 'xq1', max_top=10)
        ids = [i['_id'] for i in items]
        assert ids == ['xc10', 'xc20', 'xc30']
        # both replies resolve to root xc10
        assert items[1]['parent_comment_id'] == 'xc10'
        assert items[2]['parent_comment_id'] == 'xc10'

    def test_reply_with_unkept_root_is_dropped(self):
        # top-level A (10 likes, kept), top-level B (1 like, kept), reply to a
        # top-level that is NOT in top-100 (like 0, dropped) → reply dropped
        comments = [
            self._cm(1, 10),
            self._cm(2, 1),
            self._cm(3, 0),          # not kept (rank 3, max_top=2)
            self._cm(4, 50, in_reply_to=3),  # reply to dropped top → dropped
        ]
        items = app_module._select_top_xueqiu_items(comments, 'xq1', max_top=2)
        ids = [i['_id'] for i in items]
        assert ids == ['xc1', 'xc2']
        assert all(i['parent_comment_id'] is None for i in items)

    def test_cap_applies_to_top_level_not_replies(self):
        comments = []
        for i in range(1, 106):
            comments.append(self._cm(i, i))
        # reply attached to hottest (id 105) does not consume top-level quota
        comments.append(self._cm(1000, 0, in_reply_to=105))
        items = app_module._select_top_xueqiu_items(comments, 'xq1', max_top=100)
        assert len(items) == 101
        assert items[0]['_id'] == 'xc105'
        assert items[1]['_id'] == 'xc1000'
        assert items[1]['parent_comment_id'] == 'xc105'
        # exactly 100 top-level
        tops = [i for i in items if i['parent_comment_id'] is None]
        assert len(tops) == 100
