import json
import os
import sqlite3
import threading
import time
import fcntl
from datetime import datetime

from search import build_search_sql, highlight_all


_PS_TWEET_CONDITION = """(content LIKE '%PS图%' OR EXISTS (
    SELECT 1 FROM ps_marks WHERE ps_marks.tweet_id = tweets.id))"""


class TweetDB:
    def __init__(self, db_path=None):
        if db_path is None:
            env_path = os.environ.get('DB_PATH')
            if env_path:
                db_path = env_path
            else:
                # Always use data.db next to this module, not cwd
                db_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'data.db')
        self._lock = threading.RLock()
        self.db_path = db_path
        # Cross-process file lock to serialize writes (Flask + Scrapy subprocess)
        self._lockfile_path = db_path + '.plock'
        self._lockfile = open(self._lockfile_path, 'a')
        self.conn = sqlite3.connect(db_path, check_same_thread=False, timeout=30)
        self.conn.execute("PRAGMA foreign_keys = ON")
        self.conn.row_factory = sqlite3.Row
        self.conn.execute("PRAGMA journal_mode=WAL")
        self.conn.execute("PRAGMA busy_timeout=30000")
        self.conn.execute("PRAGMA synchronous=NORMAL")
        self._create_tables()

    def _acquire_file_lock(self):
        """Acquire exclusive cross-process lock (blocks up to 30s)."""
        deadline = time.time() + 30
        while True:
            try:
                fcntl.flock(self._lockfile, fcntl.LOCK_EX | fcntl.LOCK_NB)
                return True
            except (BlockingIOError, OSError):
                if time.time() > deadline:
                    return False
                time.sleep(0.1)

    def _release_file_lock(self):
        try:
            fcntl.flock(self._lockfile, fcntl.LOCK_UN)
        except (OSError, IOError):
            pass

    def _create_tables(self):
        with self._lock:
            self.conn.executescript("""
        CREATE TABLE IF NOT EXISTS tweets (
            id              TEXT PRIMARY KEY,
            mblogid         TEXT,
            content         TEXT NOT NULL,
            user_id         TEXT NOT NULL,
            created_at      TEXT,
            reposts_count   INTEGER DEFAULT 0,
            comments_count  INTEGER DEFAULT 0,
            attitudes_count INTEGER DEFAULT 0,
            pic_urls        TEXT,
            pic_num         INTEGER DEFAULT 0,
            source          TEXT,
            ip_location     TEXT,
            is_retweet      INTEGER DEFAULT 0,
            retweet_id      TEXT,
            deleted         INTEGER DEFAULT 0,
            deleted_at      TEXT,
            url             TEXT,
            crawl_time      INTEGER,
            platform        TEXT DEFAULT 'weibo'
        );
        """)
            self.conn.commit()
            for sql in [
                "ALTER TABLE tweets ADD COLUMN screen_name TEXT DEFAULT ''",
                "ALTER TABLE comments ADD COLUMN parent_comment_id TEXT",
                "ALTER TABLE tweets ADD COLUMN retweet_content TEXT DEFAULT ''",
                "ALTER TABLE tweets ADD COLUMN retweet_user TEXT DEFAULT ''",
            "ALTER TABLE tweets ADD COLUMN retweet_pic_urls TEXT DEFAULT '[]'",
            "ALTER TABLE tweets ADD COLUMN retweet_has_video INTEGER DEFAULT 0",
            "ALTER TABLE tweets ADD COLUMN retweet_user_id TEXT DEFAULT ''",
            "ALTER TABLE comments ADD COLUMN sort_order INTEGER DEFAULT 0",
            "ALTER TABLE annotations ADD COLUMN ranges TEXT",
            "ALTER TABLE tweets ADD COLUMN platform TEXT DEFAULT 'weibo'",
            "ALTER TABLE comments ADD COLUMN platform TEXT DEFAULT 'weibo'",
            "ALTER TABLE comments ADD COLUMN pic_urls TEXT DEFAULT '[]'",
            "ALTER TABLE tweets ADD COLUMN created_at_timezone TEXT",
            "ALTER TABLE comments ADD COLUMN created_at_timezone TEXT",
            ]:
                try:
                    self.conn.execute(sql)
                    self.conn.commit()
                except:
                    pass
            self.conn.executescript("""
        CREATE TABLE IF NOT EXISTS comments (
            id              TEXT PRIMARY KEY,
            tweet_id        TEXT NOT NULL,
            content         TEXT NOT NULL,
            created_at      TEXT,
            like_counts     INTEGER DEFAULT 0,
            ip_location     TEXT,
            comment_user    TEXT,
            reply_comment   TEXT,
            crawl_time      INTEGER,
            parent_comment_id TEXT,
            sort_order      INTEGER DEFAULT 0,
            platform        TEXT DEFAULT 'weibo',
            pic_urls        TEXT DEFAULT '[]',
            created_at_timezone TEXT
        );
        CREATE TABLE IF NOT EXISTS config (
            key   TEXT PRIMARY KEY,
            value TEXT
        );
        CREATE INDEX IF NOT EXISTS idx_tweets_user_id ON tweets(user_id);
        CREATE INDEX IF NOT EXISTS idx_tweets_created_at ON tweets(created_at);
        CREATE INDEX IF NOT EXISTS idx_tweets_deleted ON tweets(deleted);
        CREATE INDEX IF NOT EXISTS idx_comments_tweet_id ON comments(tweet_id);
        CREATE TABLE IF NOT EXISTS annotations (
            id          TEXT PRIMARY KEY,
            tweet_id    TEXT NOT NULL,
            start_offset INTEGER NOT NULL,
            end_offset   INTEGER NOT NULL,
            selected_text TEXT NOT NULL,
            comment     TEXT NOT NULL,
            field       TEXT DEFAULT 'content',
            ranges      TEXT,
            created_at  TEXT,
            updated_at  TEXT,
            FOREIGN KEY (tweet_id) REFERENCES tweets(id)
        );
        CREATE INDEX IF NOT EXISTS idx_annotations_tweet_id ON annotations(tweet_id);
        CREATE TABLE IF NOT EXISTS favorites (
            tweet_id    TEXT PRIMARY KEY,
            created_at  TEXT NOT NULL,
            FOREIGN KEY (tweet_id) REFERENCES tweets(id) ON DELETE CASCADE
        );
        CREATE TABLE IF NOT EXISTS ps_marks (
            tweet_id    TEXT PRIMARY KEY,
            created_at  TEXT NOT NULL,
            FOREIGN KEY (tweet_id) REFERENCES tweets(id) ON DELETE CASCADE
        );
        CREATE TABLE IF NOT EXISTS xueqiu_comment_queue (
            tweet_id TEXT PRIMARY KEY,
            FOREIGN KEY (tweet_id) REFERENCES tweets(id) ON DELETE CASCADE
        );
        CREATE TABLE IF NOT EXISTS operation_logs (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            timestamp   TEXT NOT NULL,
            category    TEXT NOT NULL,
            action      TEXT NOT NULL,
            detail      TEXT,
            status      TEXT,
            user        TEXT
        );
        CREATE INDEX IF NOT EXISTS idx_op_logs_ts ON operation_logs(timestamp);
        CREATE INDEX IF NOT EXISTS idx_op_logs_cat ON operation_logs(category);
        """)
            # ---- Full-text search index (FTS5 + trigram) ----
            # Migration: DBs created before the metadata columns became
            # UNINDEXED must have the FTS table rebuilt, otherwise structural
            # literals keep polluting matches. Drop it (plus the companion
            # rowid map) here, before CREATE ... IF NOT EXISTS, so the new
            # schema actually takes effect; _backfill_search_index() below
            # repopulates it.
            row = self.conn.execute(
                "SELECT sql FROM sqlite_master WHERE name='search_index'"
            ).fetchone()
            needs_fts_rebuild = bool(row and row[0]
                                     and 'doc_id UNINDEXED' not in row[0])
            if needs_fts_rebuild:
                self.conn.execute("DROP TABLE IF EXISTS search_index")
                self.conn.commit()
            # Metadata columns are UNINDEXED so their literal values
            # (`tweet`/`comment`/`annotation`, numeric ids) don't pollute
            # full-text matches — otherwise searching "tweet" hits every tweet
            # row. UNINDEXED still stores the value, so `WHERE source_type=?`,
            # snippet() and plain column reads all keep working. Do NOT use
            # content='' (contentless breaks snippet()).
            self.conn.executescript("""
        CREATE VIRTUAL TABLE IF NOT EXISTS search_index USING fts5(
            doc_id UNINDEXED,
            source_type UNINDEXED,
            tweet_id UNINDEXED,
            text,
            tokenize='trigram'
        );
        CREATE TABLE IF NOT EXISTS search_doc (
            doc_id      TEXT NOT NULL,
            source_type TEXT NOT NULL,
            fts_rowid   INTEGER NOT NULL,
            PRIMARY KEY (doc_id, source_type)
        );
        """)
            if needs_fts_rebuild:
                self.conn.execute("DELETE FROM search_doc")
            self.conn.commit()
            self._backfill_search_index()

    def _backfill_search_index(self, _lock_held=False):
        """Populate search_index from existing rows if it is empty.

        Runs on every construction until the index is populated; later writes
        keep it in sync incrementally. Also populates the search_doc rowid map
        from whatever the index holds, so an existing non-empty index (e.g. a
        DB upgraded to this version) gets its companion table filled in too.
        Pass _lock_held=True when the caller already holds the cross-process
        file lock.
        """
        with self._lock:
            if not _lock_held:
                if not self._acquire_file_lock():
                    raise RuntimeError("DB file lock timeout (search_index backfill)")
            try:
                n = self.conn.execute("SELECT COUNT(*) FROM search_index").fetchone()[0]
                if n > 0:
                    nd = self.conn.execute("SELECT COUNT(*) FROM search_doc").fetchone()[0]
                    if nd > 0:
                        return
                else:
                    self.conn.executescript("""
                    INSERT INTO search_index(doc_id, source_type, tweet_id, text)
                        SELECT id, 'tweet', id,
                               COALESCE(content,'') || ' ' || COALESCE(retweet_content,'')
                          FROM tweets;
                    INSERT INTO search_index(doc_id, source_type, tweet_id, text)
                        SELECT id, 'comment', tweet_id, COALESCE(content,'')
                          FROM comments;
                    INSERT INTO search_index(doc_id, source_type, tweet_id, text)
                        SELECT id, 'annotation', tweet_id,
                               COALESCE(comment,'') || ' ' || COALESCE(selected_text,'')
                          FROM annotations;
                    """)
                    # The index was (re)built from scratch; any stale companion
                    # rows left behind by a dropped search_index must go.
                    self.conn.execute("DELETE FROM search_doc")
                self.conn.executescript("""
                INSERT OR IGNORE INTO search_doc(doc_id, source_type, fts_rowid)
                    SELECT doc_id, source_type, rowid FROM search_index;
                """)
                self.conn.commit()
            finally:
                if not _lock_held:
                    self._release_file_lock()

    def rebuild_search_index(self):
        """Drop and rebuild the whole search index. Returns row count.

        Holds the cross-process file lock across DELETE + backfill + count so
        no concurrent writer can slip a row in between, which would make the
        backfill's empty-guard skip and silently leave a partial index.
        """
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (rebuild_search_index)")
            try:
                self.conn.execute("DELETE FROM search_index")
                self.conn.execute("DELETE FROM search_doc")
                self.conn.commit()
                self._backfill_search_index(_lock_held=True)
                return self.conn.execute(
                    "SELECT COUNT(*) FROM search_index"
                ).fetchone()[0]
            finally:
                self._release_file_lock()

    def _index_put(self, doc_id, source_type, tweet_id, text):
        """Upsert one row into search_index via the search_doc rowid map.

        Deleting by (doc_id, source_type) on a trigram FTS5 table is
        O(index_size); deleting by rowid is O(1).
        """
        row = self.conn.execute(
            "SELECT fts_rowid FROM search_doc WHERE doc_id=? AND source_type=?",
            (doc_id, source_type),
        ).fetchone()
        if row:
            self.conn.execute("DELETE FROM search_index WHERE rowid=?", (row[0],))
            self.conn.execute(
                "DELETE FROM search_doc WHERE doc_id=? AND source_type=?",
                (doc_id, source_type),
            )
        cur = self.conn.execute(
            "INSERT INTO search_index(doc_id, source_type, tweet_id, text) "
            "VALUES (?,?,?,?)",
            (doc_id, source_type, tweet_id, text or ''),
        )
        self.conn.execute(
            "INSERT INTO search_doc(doc_id, source_type, fts_rowid) VALUES (?,?,?)",
            (doc_id, source_type, cur.lastrowid),
        )

    def _index_delete(self, doc_id, source_type):
        row = self.conn.execute(
            "SELECT fts_rowid FROM search_doc WHERE doc_id=? AND source_type=?",
            (doc_id, source_type),
        ).fetchone()
        if row:
            self.conn.execute("DELETE FROM search_index WHERE rowid=?", (row[0],))
            self.conn.execute(
                "DELETE FROM search_doc WHERE doc_id=? AND source_type=?",
                (doc_id, source_type),
            )

    def _index_tweet(self, item):
        tid = str(item.get('_id') or item.get('id') or '')
        text = (item.get('content') or '') + ' ' + (item.get('retweet_content') or '')
        self._index_put(tid, 'tweet', tid, text)

    def _index_comment(self, item):
        cid = str(item.get('_id') or item.get('id') or '')
        self._index_put(cid, 'comment', str(item.get('tweet_id') or ''),
                        item.get('content') or '')

    def _index_annotation_row(self, ann_id):
        """Re-index an annotation by reading its current row."""
        row = self.conn.execute(
            "SELECT id, tweet_id, comment, selected_text FROM annotations WHERE id=?",
            (ann_id,),
        ).fetchone()
        if row is None:
            self._index_delete(ann_id, 'annotation')
            return
        text = (row['comment'] or '') + ' ' + (row['selected_text'] or '')
        self._index_put(row['id'], 'annotation', row['tweet_id'], text)

    def _batch_reindex(self, source_type, items, id_key='_id'):
        """Sync search_index/search_doc for a batch of crawled items.

        Each item must carry id_key (doc id), 'tweet_id' (may be absent for
        tweets — use the doc id), 'content', and optionally 'retweet_content'.
        Existence is checked against the search_doc rowid map so deletes are
        O(1) instead of an O(index_size) FTS5 scan. Duplicate doc_ids within
        the batch keep only the LAST occurrence (matching the tweets
        ON CONFLICT(id) DO UPDATE / comments INSERT OR REPLACE semantics).
        """
        # Dedupe keeping the LAST occurrence: re-assigning an existing dict key
        # updates the value but keeps the key's original insertion position, so
        # batch order is preserved while later duplicates win — matching the
        # tweets ON CONFLICT(id) DO UPDATE / comments INSERT OR REPLACE writes.
        deduped = {}
        for it in items:
            doc_id = str(it.get(id_key) or it.get('id') or '')
            if not doc_id:
                continue
            deduped[doc_id] = it

        existing_rids = []
        insert_rows = []
        for doc_id, it in deduped.items():
            tweet_id = str(it.get('tweet_id') or doc_id)
            if source_type == 'tweet':
                text = (it.get('content') or '') + ' ' + (it.get('retweet_content') or '')
            else:
                text = it.get('content') or ''
            row = self.conn.execute(
                "SELECT fts_rowid FROM search_doc WHERE doc_id=? AND source_type=?",
                (doc_id, source_type),
            ).fetchone()
            if row:
                existing_rids.append(row[0])
            insert_rows.append((doc_id, source_type, tweet_id, text))
        for rid in existing_rids:
            self.conn.execute("DELETE FROM search_index WHERE rowid=?", (rid,))
        if existing_rids:
            ph = ','.join(['?'] * len(existing_rids))
            self.conn.execute(
                f"DELETE FROM search_doc WHERE fts_rowid IN ({ph})",
                existing_rids,
            )
        if insert_rows:
            self.conn.executemany(
                "INSERT INTO search_index(doc_id, source_type, tweet_id, text) "
                "VALUES (?,?,?,?)",
                insert_rows,
            )
            last_rid = self.conn.execute("SELECT last_insert_rowid()").fetchone()[0]
            first_rid = last_rid - len(insert_rows) + 1
            self.conn.executemany(
                "INSERT INTO search_doc(doc_id, source_type, fts_rowid) VALUES (?,?,?)",
                [(r[0], r[1], first_rid + i) for i, r in enumerate(insert_rows)],
            )

    def close(self):
        with self._lock:
            try:
                self._release_file_lock()
            except Exception:
                pass
            try:
                self._lockfile.close()
            except Exception:
                pass
            self.conn.close()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()

    def insert_tweet(self, item):
        """Upsert by source ID; return whether a new row was inserted."""
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (insert_tweet)")
            try:
                is_new = self.conn.execute(
                    "SELECT 1 FROM tweets WHERE id=?", (item['_id'],)
                ).fetchone() is None
                self.conn.execute("""
        INSERT INTO tweets
            (id, mblogid, content, user_id, created_at, reposts_count,
             comments_count, attitudes_count, pic_urls, pic_num,
             source, ip_location, is_retweet, retweet_id, deleted,
             deleted_at, url, crawl_time, screen_name, retweet_content,
             retweet_user, retweet_user_id, retweet_pic_urls, retweet_has_video,
             platform, created_at_timezone)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            content=excluded.content,
            created_at=CASE WHEN excluded.platform='xueqiu'
                AND excluded.created_at_timezone='Asia/Shanghai'
                AND excluded.created_at IS NOT NULL
                THEN excluded.created_at ELSE tweets.created_at END,
            created_at_timezone=CASE WHEN excluded.platform='xueqiu'
                AND excluded.created_at_timezone='Asia/Shanghai'
                AND excluded.created_at IS NOT NULL
                THEN excluded.created_at_timezone ELSE tweets.created_at_timezone END,
            reposts_count=excluded.reposts_count,
            comments_count=excluded.comments_count,
            attitudes_count=excluded.attitudes_count,
            retweet_content=excluded.retweet_content,
            retweet_user=excluded.retweet_user,
            retweet_user_id=excluded.retweet_user_id,
            retweet_pic_urls=excluded.retweet_pic_urls,
            crawl_time=excluded.crawl_time,
            platform=excluded.platform
        """, (
                item['_id'], item.get('mblogid'),
                item['content'], item.get('user_id', ''),
                item.get('created_at'),
                item.get('reposts_count', 0), item.get('comments_count', 0),
                item.get('attitudes_count', 0),
                json.dumps(item.get('pic_urls', [])) if isinstance(item.get('pic_urls'), list) else (item.get('pic_urls') or '[]'),
                item.get('pic_num', 0), item.get('source', ''),
                item.get('ip_location', ''), int(item.get('is_retweet', False)),
                item.get('retweet_id'),
                int(item.get('deleted', 0)),
                datetime.now().strftime('%Y-%m-%d %H:%M:%S') if item.get('deleted') else None,
                item.get('url', ''),
                item.get('crawl_time', int(time.time())),
                item.get('screen_name', ''),
                item.get('retweet_content', ''),
                item.get('retweet_user', ''),
                item.get('retweet_user_id', ''),
                json.dumps(item.get('retweet_pic_urls', [])) if isinstance(item.get('retweet_pic_urls'), list) else (item.get('retweet_pic_urls') or '[]'),
                int(item.get('retweet_has_video', False)),
                item.get('platform', 'weibo'),
                item.get('created_at_timezone'),
            ))
                if is_new and item.get('platform') == 'xueqiu':
                    self.conn.execute(
                        "INSERT OR IGNORE INTO xueqiu_comment_queue(tweet_id) VALUES (?)",
                        (item['_id'],),
                    )
                self._index_tweet(item)
                self.conn.commit()
                return is_new
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()

    def insert_comment(self, item):
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (insert_comment)")
            try:
                self.conn.execute("""
        INSERT OR REPLACE INTO comments
            (id, tweet_id, content, created_at, like_counts,
             ip_location, comment_user, reply_comment, crawl_time, parent_comment_id, sort_order, platform, pic_urls, created_at_timezone)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """, (
                item['_id'], item['tweet_id'], item['content'],
                item.get('created_at'), item.get('like_counts', 0),
                item.get('ip_location', ''),
                json.dumps(item.get('comment_user', {})) if isinstance(item.get('comment_user'), dict) else (item.get('comment_user') or '{}'),
                json.dumps(item.get('reply_comment')) if isinstance(item.get('reply_comment'), dict) else (item.get('reply_comment')),
                item.get('crawl_time', int(time.time())),
                item.get('parent_comment_id'),
                item.get('sort_order', 0),
                item.get('platform', 'weibo'),
                json.dumps(item.get('pic_urls', [])) if isinstance(item.get('pic_urls'), list) else (item.get('pic_urls') or '[]'),
                item.get('created_at_timezone'),
            ))
                self._index_comment(item)
                self.conn.commit()
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()

    def batch_insert_tweets(self, items):
        """Insert a list of tweet items in a single transaction.

        Items are dicts produced by the tweet spider (parse_tweet_info).
        This is called by the Flask main process after reading scrapy's JSON
        output, so only this process writes to the DB.
        """
        if not items:
            return 0
        crawl_time = int(time.time())
        rows = []
        for item in items:
            is_retweet = int(item.get('is_retweet', False))
            user_id = str(item.get('user_id', ''))
            retweet_user_id = str(item.get('retweet_user_id', ''))
            if is_retweet == 0:
                deleted = 0
            elif is_retweet == 1:
                deleted = 0 if (retweet_user_id and retweet_user_id == user_id) else 1
            else:
                deleted = 1
            rows.append((
                item['_id'], item.get('mblogid'),
                item['content'], user_id,
                item.get('created_at'),
                item.get('reposts_count', 0), item.get('comments_count', 0),
                item.get('attitudes_count', 0),
                json.dumps(item.get('pic_urls', [])) if isinstance(item.get('pic_urls'), list) else (item.get('pic_urls') or '[]'),
                item.get('pic_num', 0), item.get('source', ''),
                item.get('ip_location', '') or '', is_retweet,
                item.get('retweet_id'),
                deleted,
                datetime.now().strftime('%Y-%m-%d %H:%M:%S') if deleted else None,
                item.get('url', ''),
                item.get('crawl_time', crawl_time),
                item.get('screen_name', ''),
                item.get('retweet_content', ''),
                item.get('retweet_user', ''),
                retweet_user_id,
                json.dumps(item.get('retweet_pic_urls', [])) if isinstance(item.get('retweet_pic_urls'), list) else (item.get('retweet_pic_urls') or '[]'),
                int(item.get('retweet_has_video', False)),
            ))
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (batch_insert_tweets)")
            try:
                self.conn.executemany("""
        INSERT INTO tweets
            (id, mblogid, content, user_id, created_at, reposts_count,
             comments_count, attitudes_count, pic_urls, pic_num,
             source, ip_location, is_retweet, retweet_id, deleted,
             deleted_at, url, crawl_time, screen_name, retweet_content,
             retweet_user, retweet_user_id, retweet_pic_urls, retweet_has_video)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            content=excluded.content,
            reposts_count=excluded.reposts_count,
            comments_count=excluded.comments_count,
            attitudes_count=excluded.attitudes_count,
            retweet_content=excluded.retweet_content,
            retweet_user=excluded.retweet_user,
            retweet_user_id=excluded.retweet_user_id,
            retweet_pic_urls=excluded.retweet_pic_urls,
            crawl_time=excluded.crawl_time
        """, rows)
                self._batch_reindex('tweet', items)
                self.conn.commit()
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()
        return len(rows)

    def batch_insert_comments(self, items, xueqiu_tweet_id=None):
        """Insert a list of comment items in a single transaction.

        Items are dicts produced by the comment spider.
        """
        if not items and xueqiu_tweet_id is None:
            return 0
        crawl_time = int(time.time())
        rows = []
        for item in items:
            rows.append((
                item['_id'], item['tweet_id'], item['content'],
                item.get('created_at'), item.get('like_counts', 0),
                item.get('ip_location', ''),
                json.dumps(item.get('comment_user', {})) if isinstance(item.get('comment_user'), dict) else (item.get('comment_user') or '{}'),
                json.dumps(item.get('reply_comment')) if isinstance(item.get('reply_comment'), dict) else (item.get('reply_comment')),
                item.get('crawl_time', crawl_time),
                item.get('parent_comment_id'),
                item.get('sort_order', 0),
                item.get('platform', 'weibo'),
                json.dumps(item.get('pic_urls', [])) if isinstance(item.get('pic_urls'), list) else (item.get('pic_urls') or '[]'),
                item.get('created_at_timezone'),
            ))
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (batch_insert_comments)")
            try:
                self.conn.executemany("""
        INSERT OR REPLACE INTO comments
            (id, tweet_id, content, created_at, like_counts,
             ip_location, comment_user, reply_comment, crawl_time, parent_comment_id, sort_order, platform, pic_urls, created_at_timezone)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """, rows)
                self._batch_reindex('comment', items)
                if xueqiu_tweet_id is not None:
                    # Acknowledge only after a complete, successful API fetch,
                    # including a valid empty result. Failures stay queued.
                    self.conn.execute(
                        "DELETE FROM xueqiu_comment_queue WHERE tweet_id=?",
                        (xueqiu_tweet_id,),
                    )
                self.conn.commit()
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()
        return len(rows)

    def get_tweets(self, page=1, per_page=20, sort='desc', deleted='exclude', user_id=None, platform=None):
        with self._lock:
            offset = (page - 1) * per_page
            order = 'DESC' if sort == 'desc' else 'ASC'
            conditions = []
            params = []
            if deleted == 'exclude':
                conditions.append('deleted = 0')
            elif deleted == 'only':
                conditions.append('deleted = 1')
            if user_id:
                conditions.append('user_id = ?')
                params.append(user_id)
            if platform and platform != 'all':
                conditions.append('platform = ?')
                params.append(platform)
            where = ('WHERE ' + ' AND '.join(conditions)) if conditions else ''
            sql = f"SELECT * FROM tweets {where} ORDER BY created_at {order} LIMIT ? OFFSET ?"
            rows = self.conn.execute(sql, params + [per_page, offset]).fetchall()
            results = []
            for row in rows:
                d = dict(row)
                d['pic_urls'] = json.loads(d.get('pic_urls', '[]') or '[]')
                d['retweet_pic_urls'] = json.loads(d.get('retweet_pic_urls', '[]') or '[]')
                d['is_retweet'] = bool(d.get('is_retweet'))
                d['deleted'] = bool(d.get('deleted'))
                results.append(d)
            return results

    def count_tweets(self, deleted='exclude', user_id=None, platform=None):
        """Return total number of tweets matching the same filters as get_tweets."""
        with self._lock:
            conditions = []
            params = []
            if deleted == 'exclude':
                conditions.append('deleted = 0')
            elif deleted == 'only':
                conditions.append('deleted = 1')
            if user_id:
                conditions.append('user_id = ?')
                params.append(user_id)
            if platform and platform != 'all':
                conditions.append('platform = ?')
                params.append(platform)
            where = ('WHERE ' + ' AND '.join(conditions)) if conditions else ''
            row = self.conn.execute(f"SELECT COUNT(*) FROM tweets {where}", params).fetchone()
            return row[0]

    def get_ps_tweets(self):
        """Return non-deleted PS图 posts, matched by keyword or manual mark."""
        with self._lock:
            rows = self.conn.execute(
                f"SELECT * FROM tweets WHERE deleted = 0 AND {_PS_TWEET_CONDITION} "
                "ORDER BY created_at DESC"
            ).fetchall()
            results = []
            for row in rows:
                d = dict(row)
                d['pic_urls'] = json.loads(d.get('pic_urls', '[]') or '[]')
                d['retweet_pic_urls'] = json.loads(d.get('retweet_pic_urls', '[]') or '[]')
                d['is_retweet'] = bool(d.get('is_retweet'))
                d['deleted'] = bool(d.get('deleted'))
                results.append(d)
            return results

    def get_ps_states(self, tweet_ids):
        """Return manual and effective classification using the PS feed's rule."""
        ids = list(dict.fromkeys(tweet_ids))
        states = {}
        with self._lock:
            for start in range(0, len(ids), 500):
                batch = ids[start:start + 500]
                placeholders = ','.join('?' for _ in batch)
                rows = self.conn.execute(
                    "SELECT id, EXISTS (SELECT 1 FROM ps_marks "
                    "WHERE ps_marks.tweet_id = tweets.id) AS is_ps_manual, "
                    f"{_PS_TWEET_CONDITION} AS is_ps FROM tweets "
                    f"WHERE id IN ({placeholders})", batch,
                ).fetchall()
                for row in rows:
                    states[row['id']] = {
                        'is_ps_manual': bool(row['is_ps_manual']),
                        'is_ps': bool(row['is_ps']),
                    }
        return states

    def set_ps_mark(self, tweet_id, marked):
        """Persist an explicit manual state independently of recrawled content."""
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (set_ps_mark)")
            try:
                if not self.conn.execute(
                        "SELECT 1 FROM tweets WHERE id=?", (tweet_id,)).fetchone():
                    return None
                if marked:
                    self.conn.execute(
                        "INSERT INTO ps_marks (tweet_id, created_at) VALUES (?, ?) "
                        "ON CONFLICT(tweet_id) DO NOTHING",
                        (tweet_id, datetime.now().strftime('%Y-%m-%d %H:%M:%S')),
                    )
                else:
                    self.conn.execute("DELETE FROM ps_marks WHERE tweet_id=?", (tweet_id,))
                self.conn.commit()
                return self.get_ps_states([tweet_id])[tweet_id]
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()

    def get_annotated_tweets(self):
        """Return non-deleted tweets that have at least one annotation,
        all platforms, ordered by created_at DESC.

        Compatibility helper for callers requesting notes without favorites.
        """
        return self.get_saved_tweets(kind='notes')

    def get_favorite_ids(self, tweet_ids):
        if not tweet_ids:
            return set()
        with self._lock:
            placeholders = ','.join('?' for _ in tweet_ids)
            return {row[0] for row in self.conn.execute(
                f"SELECT tweet_id FROM favorites WHERE tweet_id IN ({placeholders})",
                list(tweet_ids),
            )}

    def set_favorite(self, tweet_id, favorited):
        """Set an explicit state; retries never toggle or erase annotations."""
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (set_favorite)")
            try:
                if not self.conn.execute(
                        "SELECT 1 FROM tweets WHERE id=?", (tweet_id,)).fetchone():
                    return None
                if favorited:
                    self.conn.execute(
                        "INSERT INTO favorites (tweet_id, created_at) VALUES (?, ?) "
                        "ON CONFLICT(tweet_id) DO NOTHING",
                        (tweet_id, datetime.now().strftime('%Y-%m-%d %H:%M:%S')),
                    )
                else:
                    self.conn.execute("DELETE FROM favorites WHERE tweet_id=?", (tweet_id,))
                self.conn.commit()
                return favorited
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()

    def get_saved_tweets(self, kind='all', q='', platform='all',
                         start_date='', end_date=''):
        """Union of favorites and annotated posts, filtered before rendering."""
        if kind not in ('all', 'favorites', 'notes'):
            raise ValueError('无效的内容类型')
        if platform not in ('all', 'weibo', 'xueqiu'):
            raise ValueError('无效的平台')
        q = (q or '').strip()
        if len(q) > 200:
            raise ValueError('关键词最多 200 个字符')
        for date in (start_date, end_date):
            if date:
                try:
                    valid = datetime.strptime(date, '%Y-%m-%d').strftime('%Y-%m-%d') == date
                except ValueError:
                    valid = False
                if not valid:
                    raise ValueError('日期格式应为 YYYY-MM-DD')
        if start_date and end_date and start_date > end_date:
            raise ValueError('开始日期不能晚于结束日期')

        favorite = 'EXISTS (SELECT 1 FROM favorites f WHERE f.tweet_id=t.id)'
        annotated = 'EXISTS (SELECT 1 FROM annotations a WHERE a.tweet_id=t.id)'
        membership = {'all': f'({favorite} OR {annotated})',
                      'favorites': favorite, 'notes': annotated}[kind]
        conditions = ['t.deleted = 0', membership]
        params = []
        if platform != 'all':
            conditions.append('t.platform = ?')
            params.append(platform)
        if start_date:
            conditions.append('t.created_at >= ?')
            params.append(start_date)
        if end_date:
            conditions.append('t.created_at <= ?')
            params.append(end_date + ' 23:59:59')
        if q:
            # Literal substring matching also supports two-character Chinese
            # keywords and treats %, _ and quotes as ordinary text.
            conditions.append("""(
                instr(lower(t.content), lower(?)) > 0
                OR instr(lower(t.retweet_content), lower(?)) > 0
                OR EXISTS (SELECT 1 FROM annotations a WHERE a.tweet_id=t.id
                    AND (instr(lower(a.comment), lower(?)) > 0
                         OR instr(lower(a.selected_text), lower(?)) > 0))
            )""")
            params.extend([q] * 4)
        with self._lock:
            rows = self.conn.execute(
                f"SELECT t.*, {favorite} AS is_favorited FROM tweets t WHERE "
                + ' AND '.join(conditions) + ' ORDER BY t.created_at DESC, t.id DESC',
                params,
            ).fetchall()
            results = []
            for row in rows:
                d = dict(row)
                d['pic_urls'] = json.loads(d.get('pic_urls', '[]') or '[]')
                d['retweet_pic_urls'] = json.loads(d.get('retweet_pic_urls', '[]') or '[]')
                d['is_retweet'] = bool(d.get('is_retweet'))
                d['deleted'] = bool(d.get('deleted'))
                d['is_favorited'] = bool(d.get('is_favorited'))
                results.append(d)
            return results

    def get_tweet(self, tweet_id):
        with self._lock:
            row = self.conn.execute("SELECT * FROM tweets WHERE id=?", (tweet_id,)).fetchone()
            if row is None:
                return None
            d = dict(row)
            d['pic_urls'] = json.loads(d.get('pic_urls', '[]') or '[]')
            d['is_retweet'] = bool(d.get('is_retweet'))
            d['deleted'] = bool(d.get('deleted'))
            return d

    def get_comment_ids(self, tweet_id):
        """Snapshot stored IDs before a crawl, including replies."""
        with self._lock:
            return {r[0] for r in self.conn.execute(
                "SELECT id FROM comments WHERE tweet_id=?", (tweet_id,))}

    def get_comment_counts(self, tweet_ids):
        """Batch counts for cards and crawl responses, independent of source metadata."""
        counts = {tid: {'total': 0, 'top_level': 0, 'replies': 0} for tid in tweet_ids}
        if not counts:
            return counts
        ids = list(counts)
        with self._lock:
            rows = []
            # Notes/PS lists are unpaginated; stay below SQLite's parameter limit.
            for start in range(0, len(ids), 500):
                batch = ids[start:start + 500]
                placeholders = ','.join('?' for _ in batch)
                rows.extend(self.conn.execute(
                    "SELECT tweet_id, COUNT(*) AS total, "
                    "SUM(CASE WHEN COALESCE(parent_comment_id, '')='' THEN 1 ELSE 0 END) AS top_level "
                    f"FROM comments WHERE tweet_id IN ({placeholders}) GROUP BY tweet_id",
                    batch,
                ).fetchall())
        for row in rows:
            counts[row['tweet_id']] = {
                'total': row['total'], 'top_level': row['top_level'],
                'replies': row['total'] - row['top_level'],
            }
        return counts

    def get_comments(self, tweet_id, sort='time'):
        with self._lock:
            if sort == 'hot':
                order = 'sort_order ASC, like_counts DESC'
            else:
                order = 'created_at ASC'
            all_rows = self.conn.execute(
                f"SELECT * FROM comments WHERE tweet_id=? ORDER BY {order}",
                (tweet_id,)
            ).fetchall()
            top_comments = []
            sub_comments = {}
            for row in all_rows:
                d = dict(row)
                d['comment_user'] = json.loads(d.get('comment_user', '{}') or '{}')
                d['pic_urls'] = json.loads(d.get('pic_urls', '[]') or '[]')
                if d.get('reply_comment'):
                    d['reply_comment'] = json.loads(d['reply_comment'])
                if d.get('parent_comment_id'):
                    pid = d['parent_comment_id']
                    if pid not in sub_comments:
                        sub_comments[pid] = []
                    sub_comments[pid].append(d)
                else:
                    top_comments.append(d)
            for c in top_comments:
                c['sub_comments'] = sub_comments.get(c['id'], [])
            return top_comments

    def batch_delete(self, ids):
        with self._lock:
            if not ids:
                return 0
            placeholders = ','.join(['?'] * len(ids))
            now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (batch_delete)")
            try:
                cur = self.conn.execute(
                    f"UPDATE tweets SET deleted=1, deleted_at=? WHERE id IN ({placeholders})",
                    [now] + list(ids)
                )
                self.conn.commit()
                return cur.rowcount
            finally:
                self._release_file_lock()

    def restore_tweets(self, ids):
        with self._lock:
            if not ids:
                return 0
            placeholders = ','.join(['?'] * len(ids))
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (restore_tweets)")
            try:
                cur = self.conn.execute(
                    f"UPDATE tweets SET deleted=0, deleted_at=NULL WHERE id IN ({placeholders})",
                    list(ids)
                )
                self.conn.commit()
                return cur.rowcount
            finally:
                self._release_file_lock()

    def get_tweet_ids(self, start_date=None, end_date=None):
        """Weibo source IDs for the Weibo comment spider (never Xueqiu IDs)."""
        with self._lock:
            if start_date and end_date:
                rows = self.conn.execute(
                    "SELECT mblogid FROM tweets WHERE deleted=0 AND platform='weibo' "
                    "AND created_at >= ? AND created_at <= ?",
                    (start_date, end_date + ' 23:59:59')
                ).fetchall()
            else:
                rows = self.conn.execute(
                    "SELECT mblogid FROM tweets WHERE deleted=0 AND platform='weibo'"
                ).fetchall()
            return [r[0] for r in rows]

    def get_tweet_ids_with_enough_comments(self, min_count, start_date=None, end_date=None):
        """Tweets with enough top-level comments; replies do not consume the quota."""
        with self._lock:
            if start_date and end_date:
                rows = self.conn.execute(
                    "SELECT t.mblogid FROM tweets t "
                    "JOIN comments c ON c.tweet_id = t.id "
                    "WHERE t.deleted = 0 AND t.platform='weibo' "
                    "AND t.created_at >= ? AND t.created_at <= ? "
                    "AND COALESCE(c.parent_comment_id, '')='' "
                    "GROUP BY t.mblogid "
                    "HAVING COUNT(c.id) >= ?", (start_date, end_date + ' 23:59:59', min_count)
                ).fetchall()
            else:
                rows = self.conn.execute(
                    "SELECT t.mblogid FROM tweets t "
                    "JOIN comments c ON c.tweet_id = t.id "
                    "WHERE t.deleted = 0 AND t.platform='weibo' "
                    "AND COALESCE(c.parent_comment_id, '')='' "
                    "GROUP BY t.mblogid "
                    "HAVING COUNT(c.id) >= ?", (min_count,)
                ).fetchall()
            return [r[0] for r in rows]

    def get_latest_tweet_id(self, user_id):
        """Return the id of the most recent non-deleted tweet for a user, or None."""
        with self._lock:
            row = self.conn.execute(
                "SELECT id FROM tweets WHERE user_id=? AND deleted=0 "
                "ORDER BY created_at DESC LIMIT 1",
                (user_id,)
            ).fetchone()
            return row[0] if row else None

    def get_latest_xueqiu_id(self, user_id=None):
        """Use source ID order, independent of legacy wall-clock timezones."""
        with self._lock:
            user_filter = " AND user_id=?" if user_id else ""
            row = self.conn.execute(
                "SELECT id FROM tweets WHERE platform='xueqiu'" + user_filter +
                " ORDER BY CAST(substr(id, 3) AS INTEGER) DESC LIMIT 1",
                (user_id,) if user_id else (),
            ).fetchone()
            return row[0] if row else None

    def get_tweets_for_comment_crawl(self, hours=24):
        """Return (id, mblogid) tuples for Weibo tweets eligible for comment crawl:
        non-deleted, within the last `hours` Beijing hours, and <100 top-level comments.
        """
        with self._lock:
            rows = self.conn.execute(
                "SELECT id, mblogid FROM tweets "
                "WHERE deleted=0 AND platform='weibo' "
                # Source created_at is stored as Beijing wall time, even in UTC containers.
                "  AND created_at > datetime('now', '+8 hours', ?) "
                "  AND id NOT IN ("
                "    SELECT tweet_id FROM comments "
                "    WHERE COALESCE(parent_comment_id, '')='' "
                "    GROUP BY tweet_id HAVING COUNT(*) >= 100"
                "  ) "
                "ORDER BY created_at DESC",
                (f'-{hours} hours',)
            ).fetchall()
            return [(r[0], r[1]) for r in rows]

    def get_xueqiu_tweets_for_comment_crawl(self, ps_only=True, incremental=False):
        """Return (id, xq_id) tuples for xueqiu tweets eligible for comment crawl.

        ps_only=True limits to non-deleted xueqiu posts classified as PS图
        by keyword or manual mark. Returns stored id
        (with 'xq' prefix) and the numeric xueqiu id.
        """
        with self._lock:
            sql = ("SELECT id FROM tweets WHERE platform='xueqiu' AND deleted=0")
            if ps_only:
                sql += f" AND {_PS_TWEET_CONDITION}"
            if incremental:
                # Two daily runs cover late-arriving comments. The durable
                # queue also covers older newly discovered posts and retries.
                sql += (" AND (created_at >= datetime('now', '+8 hours', '-48 hours')"
                        " OR id IN (SELECT tweet_id FROM xueqiu_comment_queue))")
            sql += " ORDER BY created_at DESC"
            rows = self.conn.execute(sql).fetchall()
            return [(r[0], r[0][2:]) for r in rows]

    def queue_xueqiu_comments(self, tweet_ids):
        """Keep failed refreshes eligible even after they leave the 48h window."""
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError('DB file lock timeout (queue_xueqiu_comments)')
            try:
                self.conn.executemany(
                    "INSERT OR IGNORE INTO xueqiu_comment_queue(tweet_id) VALUES (?)",
                    [(tid,) for tid in tweet_ids],
                )
                self.conn.commit()
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()

    def migrate_xueqiu_timestamps(self, legacy_timezone, apply=False):
        """Explicit operator migration, never inferred from the current host.

        Only unlabelled legacy rows are eligible. New crawls label Beijing
        times, so retries and concurrent new writes cannot shift them twice.
        Caller must first verify the old database's timezone and back it up.
        """
        if legacy_timezone not in ('UTC', 'Asia/Shanghai'):
            raise ValueError('legacy_timezone must be UTC or Asia/Shanghai')
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError('DB file lock timeout (xueqiu timezone migration)')
            try:
                counts = {}
                for table in ('tweets', 'comments'):
                    where = "platform='xueqiu' AND created_at_timezone IS NULL"
                    invalid = self.conn.execute(
                        f"SELECT COUNT(*) FROM {table} WHERE {where} "
                        "AND created_at IS NOT NULL AND datetime(created_at) IS NULL"
                    ).fetchone()[0]
                    if invalid:
                        raise ValueError(f'{table}: {invalid} invalid legacy timestamps')
                    counts[table] = self.conn.execute(
                        f"SELECT COUNT(*) FROM {table} WHERE {where}"
                    ).fetchone()[0]
                    if apply:
                        value = ("datetime(created_at, '+8 hours')"
                                 if legacy_timezone == 'UTC' else 'created_at')
                        self.conn.execute(
                            f"UPDATE {table} SET created_at={value}, "
                            f"created_at_timezone='Asia/Shanghai' WHERE {where}"
                        )
                self.conn.commit()
                return counts
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()

    def delete_xueqiu_comments(self, tweet_id):
        """Delete all platform='xueqiu' comments for a tweet. Returns count deleted."""
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (delete_xueqiu_comments)")
            try:
                rows = self.conn.execute(
                    "SELECT id FROM comments WHERE tweet_id=? AND platform='xueqiu'",
                    (tweet_id,)
                ).fetchall()
                for r in rows:
                    self._index_delete(r['id'], 'comment')
                cur = self.conn.execute(
                    "DELETE FROM comments WHERE tweet_id=? AND platform='xueqiu'",
                    (tweet_id,)
                )
                self.conn.commit()
                return cur.rowcount
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()

    def prune_xueqiu_comments(self, tweet_id, max_top=100):
        """Keep only the top `max_top` hottest (by like_counts) xueqiu comments
        for a tweet, along with any child replies of kept top-level comments.
        Re-ranks kept comments' sort_order by heat (0 = hottest) so that
        get_comments(sort='hot') displays them hottest-first.
        Returns count of comments deleted.
        """
        with self._lock:
            rows = self.conn.execute(
                "SELECT id, parent_comment_id, like_counts, created_at FROM comments "
                "WHERE tweet_id=? AND platform='xueqiu'",
                (tweet_id,)
            ).fetchall()
            if not rows:
                return 0
            top_level = [r for r in rows if not r['parent_comment_id']]
            top_level.sort(key=lambda r: (-r['like_counts'], r['id']))
            keep_ids = set()
            for r in top_level[:max_top]:
                keep_ids.add(r['id'])
            # keep children whose parent is kept
            children = [r for r in rows if r['parent_comment_id']]
            keep_children = set()
            for r in children:
                if r['parent_comment_id'] in keep_ids:
                    keep_children.add(r['id'])
            keep_ids |= keep_children
            delete_ids = [r['id'] for r in rows if r['id'] not in keep_ids]
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (prune_xueqiu_comments)")
            try:
                if delete_ids:
                    placeholders = ','.join(['?'] * len(delete_ids))
                    cur = self.conn.execute(
                        f"DELETE FROM comments WHERE id IN ({placeholders})",
                        list(delete_ids)
                    )
                else:
                    cur = None
                # Re-rank kept top-level comments by heat (0 = hottest)
                rank = 0
                for r in top_level[:max_top]:
                    if r['id'] not in keep_ids:
                        continue
                    self.conn.execute(
                        "UPDATE comments SET sort_order=? WHERE id=?",
                        (rank, r['id'])
                    )
                    rank += 1
                for cid in delete_ids:
                    self._index_delete(cid, 'comment')
                self.conn.commit()
                return cur.rowcount if cur is not None else 0
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()

    def search(self, q, page=1, per_page=20, source_type='all',
               start_date=None, end_date=None):
        """Search tweets, comments and annotations for `q`, aggregated per tweet.

        Returns {'results': [...], 'total': int, 'page': int, 'per_page': int}.
        Each result is a full tweet dict plus:
          - hits:  list of {'source_type', 'doc_id' (comment/annotation only),
                            'highlight'} -- full matched text with <mark>.
          - content_hl: highlighted full tweet body (tweet hit + non-empty body).
        """
        q = (q or '').strip()
        q = ''.join(ch for ch in q if ch >= '\x20' and ch != '\x7f')
        page = max(int(page or 1), 1)
        per_page = min(max(int(per_page or 20), 1), 100)
        if not q:
            return {'results': [], 'total': 0, 'page': page, 'per_page': per_page}

        sql, params = build_search_sql(
            q, page=page, per_page=per_page, source_type=source_type,
            start_date=start_date, end_date=end_date,
        )
        with self._lock:
            rows = self.conn.execute(sql, params).fetchall()
            count_sql = "SELECT COUNT(*) FROM (" + \
                sql.replace('LIMIT ? OFFSET ?', '') + ")"
            total = self.conn.execute(count_sql, params[:-2]).fetchone()[0]
            # Backfill full matched text by doc_id from the source tables.
            comment_ids, ann_ids = [], []
            for r in rows:
                for h in json.loads(r['hits'] or '[]'):
                    if h.get('source_type') == 'comment':
                        comment_ids.append(h.get('doc_id'))
                    elif h.get('source_type') == 'annotation':
                        ann_ids.append(h.get('doc_id'))
            comment_text, ann_text = {}, {}
            if comment_ids:
                comment_ids = list(set(comment_ids))
                ph = ','.join('?' * len(comment_ids))
                comment_text = dict(self.conn.execute(
                    f"SELECT id, content FROM comments WHERE id IN ({ph})",
                    comment_ids,
                ).fetchall())
            if ann_ids:
                ann_ids = list(set(ann_ids))
                ph = ','.join('?' * len(ann_ids))
                for row in self.conn.execute(
                    f"SELECT id, comment, selected_text FROM annotations WHERE id IN ({ph})",
                    ann_ids,
                ).fetchall():
                    ann_text[row['id']] = (row['comment'] or '') + ' ' + \
                        (row['selected_text'] or '')

        _order = {'tweet': 0, 'comment': 1, 'annotation': 2}
        results = []
        for r in rows:
            d = dict(r)
            for _k in ('pic_urls', 'retweet_pic_urls'):
                if isinstance(d.get(_k), str):
                    try:
                        d[_k] = json.loads(d[_k] or '[]')
                    except (ValueError, TypeError):
                        d[_k] = []
            hits = json.loads(d.pop('hits') or '[]')
            new_hits = []
            tweet_hit = False
            for h in hits:
                st = h.get('source_type')
                if st == 'tweet':
                    tweet_hit = True
                    text = d.get('content') or ''
                elif st == 'comment':
                    text = comment_text.get(h.get('doc_id'))
                    if text is None:
                        continue
                elif st == 'annotation':
                    text = ann_text.get(h.get('doc_id'))
                    if text is None:
                        continue
                else:
                    text = ''
                h['highlight'] = highlight_all(text, q)
                new_hits.append(h)
            if not new_hits:
                continue
            new_hits.sort(key=lambda h: (_order.get(h['source_type'], 9),
                                         h.get('doc_id') or ''))
            d['hits'] = new_hits
            if tweet_hit:
                if d.get('content'):
                    d['content_hl'] = highlight_all(d['content'], q)
                if d.get('retweet_content'):
                    d['retweet_content_hl'] = highlight_all(d['retweet_content'], q)
            results.append(d)

        # Note: `total` is computed in SQL before backfill; a tweet whose every
        # hit is dropped (stale index rows) is skipped here but still counted
        # in `total`. Benign: only reachable via orphaned search_index rows.
        return {'results': results, 'total': total,
                'page': page, 'per_page': per_page}

    def stats(self):
        with self._lock:
            total = self.conn.execute("SELECT COUNT(*) FROM tweets").fetchone()[0]
            deleted_count = self.conn.execute("SELECT COUNT(*) FROM tweets WHERE deleted=1").fetchone()[0]
            comments_count = self.conn.execute("SELECT COUNT(*) FROM comments").fetchone()[0]
            return {
                'total_tweets': total,
                'deleted_tweets': deleted_count,
                'total_comments': comments_count,
            }

    def migrate_retweet_trash(self):
        """One-time migration: trash retweets of OTHER users' content.

        Original tweets (is_retweet=0) and retweets of own content
        (retweet_user_id == user_id) stay in main list.
        Retweets of others' content → trash.
        Uses screen_name as fallback for old rows lacking retweet_user_id.
        """
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (migrate_retweet_trash)")
            try:
                now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
                self.conn.execute("""
                    UPDATE tweets SET deleted=1, deleted_at=?
                    WHERE platform='weibo' AND is_retweet=1 AND deleted=0
                      AND retweet_user_id != '' AND retweet_user_id != user_id
                """, [now])
                self.conn.execute("""
                    UPDATE tweets SET deleted=1, deleted_at=?
                    WHERE platform='weibo' AND is_retweet=1 AND deleted=0
                      AND (retweet_user_id = '' OR retweet_user_id IS NULL)
                      AND retweet_user != '' AND retweet_user != screen_name
                """, [now])
                self.conn.commit()
            finally:
                self._release_file_lock()

    def get_config(self, key, default=None):
        with self._lock:
            row = self.conn.execute("SELECT value FROM config WHERE key=?", (key,)).fetchone()
            if row is None:
                return default
            val = row[0]
            if val and val.startswith('['):
                try:
                    return json.loads(val)
                except (json.JSONDecodeError, TypeError):
                    pass
            return val

    def set_config(self, key, value):
        with self._lock:
            if isinstance(value, (list, dict)):
                value = json.dumps(value)
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (set_config)")
            try:
                self.conn.execute(
                    "INSERT OR REPLACE INTO config (key, value) VALUES (?, ?)",
                    (key, str(value))
                )
                self.conn.commit()
            finally:
                self._release_file_lock()

    def insert_annotation(self, item):
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (insert_annotation)")
            try:
                now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
                self.conn.execute("""
                INSERT INTO annotations
                    (id, tweet_id, start_offset, end_offset, selected_text,
                     comment, field, ranges, created_at, updated_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """, (
                    item['id'], item['tweet_id'],
                    item['start_offset'], item['end_offset'],
                    item['selected_text'], item['comment'],
                    item.get('field', 'content'),
                    item.get('ranges'), now, now,
                ))
                self._index_annotation_row(item['id'])
                self.conn.commit()
                row = self.conn.execute(
                    "SELECT * FROM annotations WHERE id=?", (item['id'],)
                ).fetchone()
                return dict(row)
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()

    def get_annotations(self, tweet_id):
        with self._lock:
            rows = self.conn.execute(
                "SELECT * FROM annotations WHERE tweet_id=? ORDER BY start_offset ASC",
                (tweet_id,)
            ).fetchall()
            return [dict(r) for r in rows]

    def get_annotation(self, ann_id):
        with self._lock:
            row = self.conn.execute(
                "SELECT * FROM annotations WHERE id=?", (ann_id,)
            ).fetchone()
            return dict(row) if row else None

    def update_annotation(self, ann_id, comment):
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (update_annotation)")
            try:
                now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
                cur = self.conn.execute(
                    "UPDATE annotations SET comment=?, updated_at=? WHERE id=?",
                    (comment, now, ann_id)
                )
                self._index_annotation_row(ann_id)
                self.conn.commit()
                if cur.rowcount == 0:
                    return None
                row = self.conn.execute(
                    "SELECT * FROM annotations WHERE id=?", (ann_id,)
                ).fetchone()
                return dict(row)
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()

    def delete_annotation(self, ann_id):
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (delete_annotation)")
            try:
                cur = self.conn.execute(
                    "DELETE FROM annotations WHERE id=?", (ann_id,)
                )
                self._index_delete(ann_id, 'annotation')
                self.conn.commit()
                return cur.rowcount > 0
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()

    def insert_log(self, category, action, detail=None, status=None, user=None):
        """Insert a row into operation_logs.

        category: 'crawl' | 'annotation' | 'config' | 'keepalive' | 'schedule' | 'user'
        action:  short description, e.g. 'manual_incremental', 'cookie_expired'
        detail:  optional free-text context
        status:  'success' | 'failed' | 'cancelled' | 'error'
        user:    optional actor (e.g. 'scheduler', 'web', username)
        """
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (insert_log)")
            try:
                now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
                self.conn.execute(
                    "INSERT INTO operation_logs (timestamp, category, action, detail, status, user) "
                    "VALUES (?, ?, ?, ?, ?, ?)",
                    (now, category, action, detail, status, user)
                )
                self.conn.commit()
            finally:
                self._release_file_lock()

    def get_logs(self, page=1, per_page=50, category=None):
        """Return paginated operation logs, newest first."""
        with self._lock:
            offset = (page - 1) * per_page
            if category:
                rows = self.conn.execute(
                    "SELECT * FROM operation_logs WHERE category=? "
                    "ORDER BY id DESC LIMIT ? OFFSET ?",
                    (category, per_page, offset)
                ).fetchall()
                total = self.conn.execute(
                    "SELECT COUNT(*) FROM operation_logs WHERE category=?",
                    (category,)
                ).fetchone()[0]
            else:
                rows = self.conn.execute(
                    "SELECT * FROM operation_logs ORDER BY id DESC LIMIT ? OFFSET ?",
                    (per_page, offset)
                ).fetchall()
                total = self.conn.execute(
                    "SELECT COUNT(*) FROM operation_logs"
                ).fetchone()[0]
            return {'logs': [dict(r) for r in rows], 'total': total,
                    'page': page, 'per_page': per_page}

    def cleanup_old_logs(self, days=15):
        """Delete operation_logs older than `days` days. Returns deleted count."""
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (cleanup_old_logs)")
            try:
                cutoff = datetime.now().strftime('%Y-%m-%d 00:00:00')
                from datetime import timedelta as _td
                cutoff_dt = datetime.now() - _td(days=days)
                cutoff = cutoff_dt.strftime('%Y-%m-%d %H:%M:%S')
                cur = self.conn.execute(
                    "DELETE FROM operation_logs WHERE timestamp < ?",
                    (cutoff,)
                )
                self.conn.commit()
                return cur.rowcount
            finally:
                self._release_file_lock()
