import os
import sys
import tempfile
import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'weibospider'))

from db import TweetDB
from search import fts5_quote, build_search_sql, make_highlight, escape_snippet, MIN_MATCH_LEN


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


class TestBackfill:
    def test_backfill_indexes_existing_rows(self):
        """Existing tweets/comments/annotations get indexed on first upgrade."""
        fd, path = tempfile.mkstemp(suffix='.db')
        os.close(fd)
        try:
            # 1st open: write data, then drop the index to simulate an old DB
            d1 = TweetDB(path)
            d1.insert_tweet(_mk_tweet('t1', '今天量子计算有重大突破'))
            d1.insert_comment({
                '_id': 'c1', 'tweet_id': 't1', 'content': '评论提到量子计算',
                'created_at': '2024-01-01', 'like_counts': 0, 'ip_location': '',
                'comment_user': {}, 'reply_comment': None, 'crawl_time': 0,
            })
            d1.insert_annotation({
                'id': 'a1', 'tweet_id': 't1', 'start_offset': 0, 'end_offset': 2,
                'selected_text': '今天', 'comment': '我的笔记量子很重要',
                'field': 'content', 'ranges': None,
            })
            d1.conn.execute("DROP TABLE search_index")
            d1.conn.commit()
            d1.close()

            # 2nd open: _create_tables should rebuild + backfill
            d2 = TweetDB(path)
            rows = d2.conn.execute(
                "SELECT source_type, doc_id FROM search_index ORDER BY source_type"
            ).fetchall()
            got = {(r[0], r[1]) for r in rows}
            assert ('tweet', 't1') in got
            assert ('comment', 'c1') in got
            assert ('annotation', 'a1') in got
            d2.close()
        finally:
            os.unlink(path)


class TestFts5Quote:
    def test_wraps_in_double_quotes(self):
        assert fts5_quote('量子计算') == '"量子计算"'

    def test_escapes_inner_double_quote(self):
        assert fts5_quote('量子"计算') == '"量子""计算"'

    @pytest.mark.parametrize('bad', [
        '量子"计算', 'AND OR NOT', 'a*', '(paren)', 'col:val',
        "it's", '量子 计算', '-负号', '', '"',
    ])
    def test_quoted_input_never_raises_in_match(self, db, bad):
        """Any user input, once quoted, must be a legal FTS5 query."""
        db.conn.execute(
            "INSERT INTO search_index(doc_id, source_type, tweet_id, text) "
            "VALUES ('d1','tweet','t1','今天量子计算有重大突破')"
        )
        db.conn.commit()
        # must not raise OperationalError
        db.conn.execute(
            "SELECT COUNT(*) FROM search_index WHERE search_index MATCH ?",
            (fts5_quote(bad),)
        ).fetchone()

    def test_raw_input_does_raise(self, db):
        """Sanity check: without quoting, FTS5 rejects these (why we escape)."""
        import sqlite3
        with pytest.raises(sqlite3.OperationalError):
            db.conn.execute(
                "SELECT COUNT(*) FROM search_index WHERE search_index MATCH ?",
                ("it's",)
            ).fetchone()


class TestBuildSearchSql:
    def test_long_keyword_uses_match(self):
        sql, params = build_search_sql('量子计算', page=1, per_page=20)
        assert 'search_index MATCH ?' in sql
        assert 'snippet(search_index, 3' in sql
        assert params[0] == '"量子计算"'

    def test_short_keyword_uses_like_on_source_tables(self):
        sql, params = build_search_sql('量子', page=1, per_page=20)
        assert 'MATCH' not in sql
        assert 'UNION ALL' in sql
        assert '%量子%' in params

    def test_match_left_operand_is_table_not_column(self):
        """`text MATCH ?` is invalid in FTS5; must be `search_index MATCH ?`."""
        sql, _ = build_search_sql('量子计算', page=1, per_page=20)
        assert 'text MATCH' not in sql

    def test_never_orders_by_bm25(self):
        """bm25() is all -0.0000 under trigram; must sort by time."""
        for kw in ('量子计算', '量子'):
            sql, _ = build_search_sql(kw, page=1, per_page=20)
            assert 'bm25' not in sql
            assert 'created_at DESC' in sql

    def test_excludes_deleted_tweets(self):
        for kw in ('量子计算', '量子'):
            sql, _ = build_search_sql(kw, page=1, per_page=20)
            assert 'deleted' in sql

    def test_source_type_filter(self):
        sql, params = build_search_sql('量子计算', page=1, per_page=20,
                                       source_type='annotation')
        assert 'source_type' in sql
        assert 'annotation' in params

    def test_pagination_params_are_last(self):
        sql, params = build_search_sql('量子计算', page=3, per_page=10)
        assert 'LIMIT ? OFFSET ?' in sql
        assert params[-2:] == [10, 20]

    def test_date_range_filter(self):
        sql, params = build_search_sql('量子计算', page=1, per_page=20,
                                       start_date='2024-01-01',
                                       end_date='2024-12-31')
        assert '2024-01-01' in params
        assert '2024-12-31 23:59:59' in params


class TestMakeHighlight:
    def test_wraps_keyword_in_mark(self):
        out = make_highlight('今天量子计算有突破', '量子')
        assert '<mark>量子</mark>' in out

    def test_escapes_html_to_prevent_xss(self):
        out = make_highlight('<script>x</script>量子内容', '量子')
        assert '<script>' not in out
        assert '&lt;script&gt;' in out
        assert '<mark>量子</mark>' in out

    def test_truncates_long_text_around_match(self):
        text = 'A' * 200 + '量子' + 'B' * 200
        out = make_highlight(text, '量子', context=20)
        assert len(out) < 200
        assert '<mark>量子</mark>' in out
        assert out.startswith('…')
        assert out.endswith('…')

    def test_no_match_returns_truncated_head(self):
        out = make_highlight('完全无关的内容', '量子')
        assert '<mark>' not in out
        assert '完全无关的内容' in out

    def test_case_insensitive_for_ascii(self):
        out = make_highlight('Hello World', 'hello')
        assert '<mark>Hello</mark>' in out

    def test_handles_empty_inputs(self):
        assert make_highlight('', '量子') == ''
        assert '<mark>' not in make_highlight('内容', '')


class TestEscapeSnippet:
    def test_preserves_mark_and_escapes_rest(self):
        out = escape_snippet('<mark>量子</mark><script>alert(1)</script>')
        assert '<script>' not in out
        assert '&lt;script&gt;' in out
        assert '<mark>量子</mark>' in out

    def test_handles_empty(self):
        assert escape_snippet(None) is None
        assert escape_snippet('') == ''


@pytest.fixture
def seeded(db):
    """DB with one tweet + retweet, one comment, one annotation, one deleted tweet."""
    db.insert_tweet(_mk_tweet('t1', '今天量子计算有重大突破', '转发原文提到光刻机'))
    db.insert_tweet(_mk_tweet('t2', '天气不错适合出门'))
    db.insert_tweet(_mk_tweet('t3', '这条已删除但含量子计算'))
    db.insert_comment({
        '_id': 'c1', 'tweet_id': 't2', 'content': '评论里提到量子计算的事',
        'created_at': '2024-01-01', 'like_counts': 0, 'ip_location': '',
        'comment_user': {}, 'reply_comment': None, 'crawl_time': 0,
    })
    db.insert_annotation({
        'id': 'a1', 'tweet_id': 't2', 'start_offset': 0, 'end_offset': 2,
        'selected_text': '天气不错', 'comment': '我的笔记说量子计算很重要',
        'field': 'content', 'ranges': None,
    })
    db.batch_delete(['t3'])
    return db


class TestDbSearch:
    def test_finds_tweet_content(self, seeded):
        got = seeded.search('量子计算')
        assert any(r['source_type'] == 'tweet' and r['tweet_id'] == 't1'
                   for r in got['results'])

    def test_finds_retweet_content(self, seeded):
        got = seeded.search('光刻机')
        assert any(r['tweet_id'] == 't1' for r in got['results'])

    def test_finds_comment_content(self, seeded):
        got = seeded.search('量子计算')
        assert any(r['source_type'] == 'comment' and r['doc_id'] == 'c1'
                   for r in got['results'])

    def test_finds_annotation_comment(self, seeded):
        got = seeded.search('笔记说量子')
        assert any(r['source_type'] == 'annotation' and r['doc_id'] == 'a1'
                   for r in got['results'])

    def test_finds_annotation_selected_text(self, seeded):
        got = seeded.search('天气不错')
        assert any(r['source_type'] == 'annotation' for r in got['results'])

    def test_excludes_deleted_tweets(self, seeded):
        got = seeded.search('量子计算')
        assert all(r['tweet_id'] != 't3' for r in got['results'])

    def test_short_keyword_works(self, seeded):
        """2-char keyword must still find results (LIKE path)."""
        got = seeded.search('量子')
        assert got['total'] > 0
        assert any(r['tweet_id'] == 't1' for r in got['results'])

    def test_short_keyword_highlights_matched_source(self, seeded):
        """LIKE path highlight must show the comment's matched text, not the tweet body."""
        # Index the seeded comment manually (sync maintenance lands in Task 7)
        seeded.conn.execute(
            "INSERT INTO search_index(doc_id, source_type, tweet_id, text) "
            "VALUES ('c1','comment','t2','评论里提到量子计算的事')"
        )
        seeded.conn.commit()
        got = seeded.search('量子')
        hits = [r for r in got['results'] if r['source_type'] == 'comment']
        assert hits, "expected a comment hit"
        assert '评论里提到' in hits[0]['highlight']
        assert '<mark>量子</mark>' in hits[0]['highlight']
        assert '天气不错' not in hits[0]['highlight']  # tweet body text must NOT appear

    def test_short_keyword_has_highlight(self, seeded):
        got = seeded.search('量子')
        assert any('<mark>' in (r.get('highlight') or '') for r in got['results'])

    def test_long_keyword_has_highlight(self, seeded):
        got = seeded.search('量子计算')
        assert any('<mark>' in (r.get('highlight') or '') for r in got['results'])

    def test_source_type_filter(self, seeded):
        got = seeded.search('量子计算', source_type='comment')
        assert got['results']
        assert all(r['source_type'] == 'comment' for r in got['results'])

    def test_special_chars_do_not_raise(self, seeded):
        for bad in ["it's", '量子"计算', '-负号', 'AND', 'a*', 'col:val', '(x)']:
            got = seeded.search(bad)
            assert 'results' in got

    def test_empty_query_returns_empty(self, seeded):
        got = seeded.search('')
        assert got['results'] == []
        assert got['total'] == 0

    def test_pagination(self, seeded):
        got = seeded.search('量子计算', page=1, per_page=1)
        assert len(got['results']) <= 1
        assert got['page'] == 1
        assert got['per_page'] == 1

    def test_total_reflects_all_matches(self, seeded):
        got = seeded.search('量子计算', page=1, per_page=1)
        assert got['total'] >= 2  # tweet t1 + comment c1

    def test_new_write_is_searchable_immediately(self, seeded):
        """Sync index maintenance: a fresh insert is searchable at once."""
        seeded.insert_tweet(_mk_tweet('t9', '全新内容超导材料研究'))
        got = seeded.search('超导材料')
        assert any(r['tweet_id'] == 't9' for r in got['results'])

    def test_annotation_update_is_searchable(self, seeded):
        seeded.update_annotation('a1', '改后的笔记提到石墨烯')
        got = seeded.search('石墨烯')
        assert any(r['doc_id'] == 'a1' for r in got['results'])

    def test_annotation_delete_removes_from_index(self, seeded):
        seeded.delete_annotation('a1')
        got = seeded.search('笔记说量子')
        assert all(r['doc_id'] != 'a1' for r in got['results'])

    def test_control_chars_in_query_do_not_raise(self, seeded):
        """Control chars (e.g. from pasted text) must not cause a 500."""
        got = seeded.search('a\x00b\x00c')
        assert 'results' in got
        got2 = seeded.search('量子\x00计算')
        assert 'results' in got2


class TestSearchApiContract:
    """The route is a thin wrapper; verify it exists and clamps params."""

    def test_route_registered(self):
        import app as app_module
        rules = {r.rule for r in app_module.app.url_map.iter_rules()}
        assert '/api/search' in rules

    def test_per_page_is_clamped(self, seeded):
        got = seeded.search('量子计算', per_page=9999)
        assert got['per_page'] <= 100

    def test_page_floor_is_one(self, seeded):
        got = seeded.search('量子计算', page=0)
        assert got['page'] == 1

    def test_response_shape(self, seeded):
        got = seeded.search('量子计算')
        assert set(['results', 'total', 'page', 'per_page']).issubset(got.keys())
        if got['results']:
            r = got['results'][0]
            for key in ('doc_id', 'source_type', 'tweet_id', 'highlight',
                        'content', 'created_at', 'screen_name'):
                assert key in r


class TestReindex:
    def test_reindex_route_registered(self):
        import app as app_module
        rules = {r.rule for r in app_module.app.url_map.iter_rules()}
        assert '/api/search/reindex' in rules

    def test_rebuild_search_index_repopulates(self, seeded):
        seeded.conn.execute("DELETE FROM search_index")
        seeded.conn.commit()
        assert seeded.search('量子计算')['total'] == 0
        n = seeded.rebuild_search_index()
        assert n > 0
        assert seeded.search('量子计算')['total'] > 0


class TestSearchDocConsistency:
    def _assert_sync(self, db):
        """search_doc and search_index must have equal counts and no orphans."""
        n_fts = db.conn.execute("SELECT COUNT(*) FROM search_index").fetchone()[0]
        n_map = db.conn.execute("SELECT COUNT(*) FROM search_doc").fetchone()[0]
        assert n_fts == n_map, f'index={n_fts} map={n_map}'
        orphans = db.conn.execute(
            "SELECT COUNT(*) FROM search_doc d LEFT JOIN search_index i "
            "ON d.fts_rowid = i.rowid WHERE i.rowid IS NULL"
        ).fetchone()[0]
        assert orphans == 0, f'{orphans} orphan map rows'

    def test_sync_after_batch_mixed(self, seeded):
        """Mixed new+updated batch keeps the map in sync."""
        items = []
        for i in range(50):
            items.append({
                '_id': f'nc{i}', 'tweet_id': 't1', 'content': f'新评论 {i} 量子计算',
                'created_at': '2024-01-01', 'like_counts': 0, 'ip_location': '',
                'comment_user': {}, 'reply_comment': None, 'crawl_time': 0,
            })
        # c1 already exists from seeded fixture (update it too)
        items.append({
            '_id': 'c1', 'tweet_id': 't2', 'content': '量子计算 更新后的评论',
            'created_at': '2024-01-01', 'like_counts': 0, 'ip_location': '',
            'comment_user': {}, 'reply_comment': None, 'crawl_time': 0,
        })
        seeded.batch_insert_comments(items)
        self._assert_sync(seeded)

    def test_sync_after_annotation_lifecycle(self, seeded):
        seeded.insert_annotation({
            'id': 'a9', 'tweet_id': 't1', 'start_offset': 0, 'end_offset': 2,
            'selected_text': '今天', 'comment': '量子计算 笔记', 'field': 'content', 'ranges': None,
        })
        self._assert_sync(seeded)
        seeded.update_annotation('a9', '量子计算 改后的笔记')
        self._assert_sync(seeded)
        seeded.delete_annotation('a9')
        self._assert_sync(seeded)

    def test_sync_after_duplicate_doc_in_batch(self, seeded):
        """Duplicate doc_ids in one batch must not crash or desync."""
        items = [
            {'_id': 'dup1', 'tweet_id': 't1', 'content': '重复一 量子计算',
             'created_at': '2024-01-01', 'like_counts': 0, 'ip_location': '',
             'comment_user': {}, 'reply_comment': None, 'crawl_time': 0},
            {'_id': 'dup1', 'tweet_id': 't1', 'content': '重复二 量子计算',
             'created_at': '2024-01-01', 'like_counts': 0, 'ip_location': '',
             'comment_user': {}, 'reply_comment': None, 'crawl_time': 0},
        ]
        seeded.batch_insert_comments(items)
        self._assert_sync(seeded)
        # the last duplicate wins; index has exactly one dup1 row
        n = seeded.conn.execute(
            "SELECT COUNT(*) FROM search_index WHERE doc_id='dup1' AND source_type='comment'"
        ).fetchone()[0]
        assert n == 1

    def test_upgrade_populate_with_legacy_duplicates(self):
        """A legacy index with duplicate (doc_id, source_type) must not crash boot."""
        import tempfile
        fd, path = tempfile.mkstemp(suffix='.db')
        os.close(fd)
        try:
            import sqlite3
            c = sqlite3.connect(path)
            c.executescript("""
            CREATE TABLE tweets(id TEXT PRIMARY KEY, content TEXT, retweet_content TEXT DEFAULT '', screen_name TEXT, created_at TEXT, deleted INTEGER DEFAULT 0, platform TEXT DEFAULT 'weibo', user_id TEXT);
            CREATE TABLE comments(id TEXT PRIMARY KEY, tweet_id TEXT, content TEXT, created_at TEXT);
            CREATE TABLE annotations(id TEXT PRIMARY KEY, tweet_id TEXT, comment TEXT, selected_text TEXT);
            CREATE VIRTUAL TABLE search_index USING fts5(doc_id, source_type, tweet_id, text, tokenize='trigram');
            INSERT INTO search_index(doc_id, source_type, tweet_id, text) VALUES ('c1','comment','t1','第一条重复');
            INSERT INTO search_index(doc_id, source_type, tweet_id, text) VALUES ('c1','comment','t1','第二条重复');
            INSERT INTO tweets VALUES('t1','内容','','博主','2024-01-01',0,'weibo','u1');
            """)
            c.commit()
            c.close()
            # must not raise
            db = TweetDB(path)
            db.close()
        finally:
            os.unlink(path)
