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


def _mk_tweet(tid, content, retweet_content=''):
    return {
        '_id': tid, 'mblogid': 'Mb' + tid, 'user_id': '1087770692',
        'content': content, 'created_at': '2024-01-01 12:00:00',
        'reposts_count': 0, 'comments_count': 0, 'attitudes_count': 0,
        'pic_urls': '[]', 'pic_num': 0, 'source': '', 'ip_location': '',
        'is_retweet': 0, 'retweet_id': None, 'url': '', 'crawl_time': 0,
        'screen_name': '博主A', 'retweet_content': retweet_content,
    }


class TestSearchIndexTable:
    def test_search_index_table_exists(self, db):
        """search_index FTS5 table should be created on init."""
        tables = [r[0] for r in db.conn.execute(
            "SELECT name FROM sqlite_master WHERE name='search_index'"
        ).fetchall()]
        assert 'search_index' in tables

    def test_search_index_uses_trigram(self, db):
        """Table must use trigram tokenizer (MATCH on 3+ chars works)."""
        db.conn.execute(
            "INSERT INTO search_index(doc_id, source_type, tweet_id, text) "
            "VALUES ('d1','tweet','t1','今天量子计算有重大突破')"
        )
        db.conn.commit()
        n = db.conn.execute(
            "SELECT COUNT(*) FROM search_index WHERE search_index MATCH ?",
            ('量子计',)
        ).fetchone()[0]
        assert n == 1

    def test_snippet_works(self, db):
        """snippet() must return highlighted text, not None (contentless mode check)."""
        db.conn.execute(
            "INSERT INTO search_index(doc_id, source_type, tweet_id, text) "
            "VALUES ('d1','tweet','t1','今天量子计算有重大突破')"
        )
        db.conn.commit()
        hl = db.conn.execute(
            "SELECT snippet(search_index, 3, '<mark>', '</mark>', '…', 12) "
            "FROM search_index WHERE search_index MATCH ?",
            ('量子计',)
        ).fetchone()[0]
        assert hl is not None
        assert '<mark>' in hl
