import os
import sys
import tempfile
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))

from db import TweetDB


@pytest.fixture
def db():
    """Create a test database in a temp file."""
    fd, path = tempfile.mkstemp(suffix='.db')
    os.close(fd)
    tdb = TweetDB(path)
    yield tdb
    tdb.close()
    os.unlink(path)


class TestTweetDB:
    def test_create_tables(self, db):
        """Tables should be created on init."""
        tables = db.conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table'"
        ).fetchall()
        table_names = [t[0] for t in tables]
        assert 'tweets' in table_names
        assert 'comments' in table_names

    def test_insert_tweet(self, db):
        db.insert_tweet({
            '_id': '123456', 'mblogid': 'Mb123', 'user_id': '1087770692',
            'content': 'hello world', 'created_at': '2024-01-01 12:00:00',
            'reposts_count': 0, 'comments_count': 2, 'attitudes_count': 10,
            'pic_urls': '[]', 'pic_num': 0, 'source': 'iPhone',
            'ip_location': '北京', 'is_retweet': 0, 'retweet_id': None,
            'url': 'https://weibo.com/1087770692/Mb123', 'crawl_time': 1700000000,
        })
        row = db.conn.execute("SELECT * FROM tweets WHERE id='123456'").fetchone()
        assert row is not None
        assert row[2] == 'hello world'
        assert row[14] == 0  # deleted=0

    def test_insert_tweet_ignore_duplicate(self, db):
        tweet = {
            '_id': '123456', 'mblogid': 'Mb123', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 12:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        }
        db.insert_tweet(tweet)
        db.insert_tweet(tweet)
        count = db.conn.execute("SELECT COUNT(*) FROM tweets WHERE id='123456'").fetchone()[0]
        assert count == 1

    def test_insert_comment(self, db):
        db.insert_tweet({
            '_id': '123456', 'mblogid': 'Mb123', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 12:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_comment({
            '_id': 'c1', 'tweet_id': '123456', 'content': 'nice',
            'created_at': '2024-01-01 13:00:00', 'like_counts': 5,
            'ip_location': '上海', 'comment_user': '{"nick_name":"A"}',
            'reply_comment': None, 'crawl_time': 1700000000,
        })
        row = db.conn.execute("SELECT * FROM comments WHERE id='c1'").fetchone()
        assert row is not None
        assert row['tweet_id'] == '123456'
        assert row['content'] == 'nice'

    def test_get_tweets_pagination(self, db):
        for i in range(5):
            db.insert_tweet({
                '_id': str(i), 'mblogid': f'Mb{i}', 'user_id': '1087770692',
                'content': f'tweet {i}', 'created_at': f'2024-01-01 0{i}:00:00',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })
        page1 = db.get_tweets(page=1, per_page=2, sort='desc')
        assert len(page1) == 2
        assert page1[0]['id'] == '4'  # newest first
        page2 = db.get_tweets(page=2, per_page=2, sort='desc')
        assert len(page2) == 2
        assert page2[0]['id'] == '2'
        page3 = db.get_tweets(page=3, per_page=2, sort='desc')
        assert len(page3) == 1
        assert page3[0]['id'] == '0'

    def test_get_tweets_excludes_deleted(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'kept', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_tweet({
            '_id': '2', 'mblogid': 'Mb2', 'user_id': '1087770692',
            'content': 'deleted', 'created_at': '2024-01-01 11:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.batch_delete(['2'])
        results = db.get_tweets(page=1, per_page=10)
        assert len(results) == 1
        assert results[0]['id'] == '1'

    def test_get_tweets_deleted(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'deleted', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.batch_delete(['1'])
        results = db.get_tweets(page=1, per_page=10, deleted='all')
        assert len(results) == 1
        assert results[0]['deleted'] == 1

    def test_get_comments(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        for i in range(3):
            db.insert_comment({
                '_id': f'c{i}', 'tweet_id': '1', 'content': f'comment {i}',
                'created_at': f'2024-01-01 1{i}:00:00', 'like_counts': 0,
                'ip_location': '', 'comment_user': '{}',
                'reply_comment': None, 'crawl_time': 0,
            })
        comments = db.get_comments('1')
        assert len(comments) == 3

    def test_batch_delete(self, db):
        for i in range(3):
            db.insert_tweet({
                '_id': str(i), 'mblogid': f'Mb{i}', 'user_id': '1087770692',
                'content': f'tweet {i}', 'created_at': '2024-01-01 0{i}:00:00',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })
        count = db.batch_delete(['0', '1'])
        assert count == 2
        for id_ in ['0', '1']:
            row = db.conn.execute(f"SELECT deleted FROM tweets WHERE id='{id_}'").fetchone()
            assert row[0] == 1
        row = db.conn.execute("SELECT deleted FROM tweets WHERE id='2'").fetchone()
        assert row[0] == 0

    def test_restore_tweets(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.batch_delete(['1'])
        count = db.restore_tweets(['1'])
        assert count == 1
        row = db.conn.execute("SELECT deleted FROM tweets WHERE id='1'").fetchone()
        assert row[0] == 0

    def test_get_tweet(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': '1087770692',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        tweet = db.get_tweet('1')
        assert tweet is not None
        assert tweet['content'] == 'hello'

    def test_get_tweet_ids(self, db):
        for i in range(3):
            db.insert_tweet({
                '_id': str(i), 'mblogid': f'Mb{i}', 'user_id': '1087770692',
                'content': f'tweet {i}', 'created_at': '2024-01-01 0{i}:00:00',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })
        ids = db.get_tweet_ids()
        assert ids == ['Mb0', 'Mb1', 'Mb2']

    def test_stats(self, db):
        for i in range(5):
            db.insert_tweet({
                '_id': str(i), 'mblogid': f'Mb{i}', 'user_id': '1087770692',
                'content': f'tweet {i}', 'created_at': '2024-01-01 0{i}:00:00',
                'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
                'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
                'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
            })
        db.batch_delete(['0', '1'])
        db.insert_comment({
            '_id': 'c1', 'tweet_id': '2', 'content': 'nice',
            'created_at': '2024-01-01 13:00:00', 'like_counts': 0,
            'ip_location': '', 'comment_user': '{}',
            'reply_comment': None, 'crawl_time': 0,
        })
        stats = db.stats()
        assert stats['total_tweets'] == 5
        assert stats['deleted_tweets'] == 2
        assert stats['total_comments'] == 1

    def test_get_latest_tweet_id(self, db):
        db.insert_tweet({
            '_id': '111', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'old', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_tweet({
            '_id': '222', 'mblogid': 'Mb2', 'user_id': 'u1',
            'content': 'new', 'created_at': '2024-06-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        assert db.get_latest_tweet_id('u1') == '222'

    def test_get_latest_tweet_id_no_tweets(self, db):
        assert db.get_latest_tweet_id('nonexistent') is None

    def test_get_latest_tweet_id_excludes_deleted(self, db):
        db.insert_tweet({
            '_id': '111', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'deleted', 'created_at': '2024-06-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.batch_delete(['111'])
        assert db.get_latest_tweet_id('u1') is None

    def test_get_tweets_for_comment_crawl(self, db):
        from datetime import datetime, timedelta
        recent = (datetime.now() - timedelta(hours=2)).strftime('%Y-%m-%d %H:%M:%S')
        old = (datetime.now() - timedelta(hours=20)).strftime('%Y-%m-%d %H:%M:%S')
        # recent tweet, no comments → should be included
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'recent', 'created_at': recent,
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        # 10h-old tweet → should be excluded (outside 8h window)
        tenh = (datetime.now() - timedelta(hours=10)).strftime('%Y-%m-%d %H:%M:%S')
        db.insert_tweet({
            '_id': '5', 'mblogid': 'Mb5', 'user_id': 'u1',
            'content': '10h old', 'created_at': tenh,
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        # old tweet → should be excluded
        db.insert_tweet({
            '_id': '2', 'mblogid': 'Mb2', 'user_id': 'u1',
            'content': 'old', 'created_at': old,
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        # recent tweet with 100 comments → should be excluded (frozen)
        db.insert_tweet({
            '_id': '3', 'mblogid': 'Mb3', 'user_id': 'u1',
            'content': 'frozen', 'created_at': recent,
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        for i in range(100):
            db.insert_comment({
                '_id': f'c{i}', 'tweet_id': '3', 'content': f'comment {i}',
                'created_at': recent, 'like_counts': 0, 'ip_location': '',
                'comment_user': '{}', 'crawl_time': 0,
            })
        # deleted recent tweet → should be excluded
        db.insert_tweet({
            '_id': '4', 'mblogid': 'Mb4', 'user_id': 'u1',
            'content': 'deleted', 'created_at': recent,
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.batch_delete(['4'])

        results = db.get_tweets_for_comment_crawl(hours=8)
        ids = [r[0] for r in results]
        assert '1' in ids
        assert '2' not in ids
        assert '3' not in ids
        assert '4' not in ids
        assert '5' not in ids


class TestAnnotations:
    def test_create_table(self, db):
        tables = db.conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table'"
        ).fetchall()
        assert 'annotations' in [t[0] for t in tables]

    def test_insert_annotation(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'hello world', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        ann = db.insert_annotation({
            'id': 'a1', 'tweet_id': '1', 'start_offset': 0,
            'end_offset': 5, 'selected_text': 'hello',
            'comment': 'hi', 'field': 'content',
        })
        assert ann['id'] == 'a1'
        assert ann['selected_text'] == 'hello'
        assert ann['comment'] == 'hi'

    def test_get_annotations(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_annotation({
            'id': 'a1', 'tweet_id': '1', 'start_offset': 0,
            'end_offset': 3, 'selected_text': 'hel', 'comment': 'c1', 'field': 'content',
        })
        db.insert_annotation({
            'id': 'a2', 'tweet_id': '1', 'start_offset': 3,
            'end_offset': 5, 'selected_text': 'lo', 'comment': 'c2', 'field': 'content',
        })
        anns = db.get_annotations('1')
        assert len(anns) == 2
        assert anns[0]['comment'] == 'c1'

    def test_update_annotation(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_annotation({
            'id': 'a1', 'tweet_id': '1', 'start_offset': 0,
            'end_offset': 3, 'selected_text': 'hel', 'comment': 'old', 'field': 'content',
        })
        updated = db.update_annotation('a1', 'new comment')
        assert updated['comment'] == 'new comment'
        anns = db.get_annotations('1')
        assert anns[0]['comment'] == 'new comment'

    def test_delete_annotation(self, db):
        db.insert_tweet({
            '_id': '1', 'mblogid': 'Mb1', 'user_id': 'u1',
            'content': 'hello', 'created_at': '2024-01-01 10:00:00',
            'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
            'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
            'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        })
        db.insert_annotation({
            'id': 'a1', 'tweet_id': '1', 'start_offset': 0,
            'end_offset': 3, 'selected_text': 'hel', 'comment': 'c1', 'field': 'content',
        })
        assert db.delete_annotation('a1') is True
        assert db.delete_annotation('a1') is False
        assert len(db.get_annotations('1')) == 0
