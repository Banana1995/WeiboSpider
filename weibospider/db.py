import json
import os
import sqlite3
import threading
import time
import fcntl
from datetime import datetime

from search import build_search_sql, make_highlight, escape_snippet


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
            pic_urls        TEXT DEFAULT '[]'
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
            # NOTE: all columns indexed (no UNINDEXED) so `WHERE source_type=?`
            # works; do NOT use content='' (contentless breaks snippet()).
            self.conn.executescript("""
        CREATE VIRTUAL TABLE IF NOT EXISTS search_index USING fts5(
            doc_id,
            source_type,
            tweet_id,
            text,
            tokenize='trigram'
        );
        """)
            self.conn.commit()
            self._backfill_search_index()

    def _backfill_search_index(self):
        """Populate search_index from existing rows if it is empty.

        Runs on every construction until the index is non-empty; later writes
        keep it in sync incrementally.
        """
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (search_index backfill)")
            try:
                n = self.conn.execute("SELECT COUNT(*) FROM search_index").fetchone()[0]
                if n > 0:
                    return
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
                self.conn.commit()
            finally:
                self._release_file_lock()

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
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (insert_tweet)")
            try:
                self.conn.execute("""
        INSERT INTO tweets
            (id, mblogid, content, user_id, created_at, reposts_count,
             comments_count, attitudes_count, pic_urls, pic_num,
             source, ip_location, is_retweet, retweet_id, deleted,
             deleted_at, url, crawl_time, screen_name, retweet_content,
             retweet_user, retweet_user_id, retweet_pic_urls, retweet_has_video,
             platform)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            content=excluded.content,
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
            ))
                self.conn.commit()
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
             ip_location, comment_user, reply_comment, crawl_time, parent_comment_id, sort_order, platform, pic_urls)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
            ))
                self.conn.commit()
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
                self.conn.commit()
            except Exception:
                self.conn.rollback()
                raise
            finally:
                self._release_file_lock()
        return len(rows)

    def batch_insert_comments(self, items):
        """Insert a list of comment items in a single transaction.

        Items are dicts produced by the comment spider.
        """
        if not items:
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
            ))
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (batch_insert_comments)")
            try:
                self.conn.executemany("""
        INSERT OR REPLACE INTO comments
            (id, tweet_id, content, created_at, like_counts,
             ip_location, comment_user, reply_comment, crawl_time, parent_comment_id, sort_order, platform, pic_urls)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """, rows)
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
        """Return non-deleted monthly 'PS图' summary tweets (content contains 'PS图').

        Used by the PS图 tab to show the blogger's monthly trading summaries.
        """
        with self._lock:
            rows = self.conn.execute(
                "SELECT * FROM tweets WHERE deleted = 0 AND content LIKE '%PS图%' "
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

    def get_annotated_tweets(self):
        """Return non-deleted tweets that have at least one annotation,
        all platforms, ordered by created_at DESC.

        Used by the 笔记 tab to show tweets that have 划线评论.
        """
        with self._lock:
            rows = self.conn.execute(
                "SELECT * FROM tweets WHERE deleted = 0 "
                "AND id IN (SELECT DISTINCT tweet_id FROM annotations) "
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
        with self._lock:
            if start_date and end_date:
                rows = self.conn.execute(
                    "SELECT mblogid FROM tweets WHERE deleted=0 "
                    "AND created_at >= ? AND created_at <= ?",
                    (start_date, end_date + ' 23:59:59')
                ).fetchall()
            else:
                rows = self.conn.execute("SELECT mblogid FROM tweets WHERE deleted=0").fetchall()
            return [r[0] for r in rows]

    def get_tweet_ids_with_enough_comments(self, min_count, start_date=None, end_date=None):
        with self._lock:
            if start_date and end_date:
                rows = self.conn.execute(
                    "SELECT t.mblogid FROM tweets t "
                    "JOIN comments c ON c.tweet_id = t.id "
                    "WHERE t.deleted = 0 AND t.created_at >= ? AND t.created_at <= ? "
                    "GROUP BY t.mblogid "
                    "HAVING COUNT(c.id) >= ?", (start_date, end_date + ' 23:59:59', min_count)
                ).fetchall()
            else:
                rows = self.conn.execute(
                    "SELECT t.mblogid FROM tweets t "
                    "JOIN comments c ON c.tweet_id = t.id "
                    "WHERE t.deleted = 0 "
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

    def get_latest_xueqiu_id(self):
        """Return the most recently crawled xueqiu post id, or None."""
        with self._lock:
            row = self.conn.execute(
                "SELECT id FROM tweets WHERE platform='xueqiu' "
                "ORDER BY created_at DESC LIMIT 1"
            ).fetchone()
            return row[0] if row else None

    def get_tweets_for_comment_crawl(self, hours=8):
        """Return (id, mblogid) tuples for tweets eligible for comment crawl:
        non-deleted, within the last `hours` hours, and with <100 comments.
        """
        with self._lock:
            rows = self.conn.execute(
                "SELECT id, mblogid FROM tweets "
                "WHERE deleted=0 "
                "  AND created_at > datetime('now', 'localtime', ?) "
                "  AND id NOT IN ("
                "    SELECT tweet_id FROM comments "
                "    GROUP BY tweet_id HAVING COUNT(*) >= 100"
                "  ) "
                "ORDER BY created_at DESC",
                (f'-{hours} hours',)
            ).fetchall()
            return [(r[0], r[1]) for r in rows]

    def get_xueqiu_tweets_for_comment_crawl(self, ps_only=True):
        """Return (id, xq_id) tuples for xueqiu tweets eligible for comment crawl.

        ps_only=True limits to non-deleted xueqiu posts whose content mentions
        'PS图' (the small-batch target for comment crawling). Returns stored id
        (with 'xq' prefix) and the numeric xueqiu id.
        """
        with self._lock:
            sql = ("SELECT id FROM tweets WHERE platform='xueqiu' AND deleted=0")
            params = []
            if ps_only:
                sql += " AND content LIKE ?"
                params.append('%PS图%')
            sql += " ORDER BY created_at DESC"
            rows = self.conn.execute(sql, params).fetchall()
            return [(r[0], r[0][2:]) for r in rows]

    def delete_xueqiu_comments(self, tweet_id):
        """Delete all platform='xueqiu' comments for a tweet. Returns count deleted."""
        with self._lock:
            if not self._acquire_file_lock():
                raise RuntimeError("DB file lock timeout (delete_xueqiu_comments)")
            try:
                cur = self.conn.execute(
                    "DELETE FROM comments WHERE tweet_id=? AND platform='xueqiu'",
                    (tweet_id,)
                )
                self.conn.commit()
                return cur.rowcount
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
                self.conn.commit()
                return cur.rowcount if cur is not None else 0
            finally:
                self._release_file_lock()

    def search(self, q, page=1, per_page=20, source_type='all',
               start_date=None, end_date=None):
        """Search tweets, comments and annotations for `q`.

        Returns {'results': [...], 'total': int, 'page': int, 'per_page': int}.
        """
        q = (q or '').strip()
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
            # total: same query without LIMIT/OFFSET, wrapped in COUNT(*)
            count_sql = "SELECT COUNT(*) FROM (" + \
                sql.replace('LIMIT ? OFFSET ?', '') + ")"
            total = self.conn.execute(count_sql, params[:-2]).fetchone()[0]

        results = []
        for r in rows:
            d = dict(r)
            if not d.get('highlight'):
                # LIKE path (or snippet miss): highlight in Python
                d['highlight'] = make_highlight(d.get('content') or '', q)
            else:
                # IMPORTANT: snippet() does NOT HTML-escape tweet content.
                # Escape it now and restore the <mark> markers, else raw
                # `<script>` in a tweet becomes stored XSS in innerHTML.
                d['highlight'] = escape_snippet(d['highlight'])
            results.append(d)
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
                self.conn.commit()
                row = self.conn.execute(
                    "SELECT * FROM annotations WHERE id=?", (item['id'],)
                ).fetchone()
                return dict(row)
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
                self.conn.commit()
                if cur.rowcount == 0:
                    return None
                row = self.conn.execute(
                    "SELECT * FROM annotations WHERE id=?", (ann_id,)
                ).fetchone()
                return dict(row)
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
                self.conn.commit()
                return cur.rowcount > 0
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
