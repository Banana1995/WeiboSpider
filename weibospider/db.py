import json
import os
import sqlite3
import threading
import time
from datetime import datetime


class TweetDB:
    def __init__(self, db_path=None):
        if db_path is None:
            db_path = os.path.join(os.getcwd(), 'data.db')
        self._lock = threading.RLock()
        self.conn = sqlite3.connect(db_path, check_same_thread=False, timeout=10)
        self.conn.execute("PRAGMA foreign_keys = ON")
        self.conn.row_factory = sqlite3.Row
        self.conn.execute("PRAGMA journal_mode=WAL")
        self.conn.execute("PRAGMA busy_timeout=10000")
        self.conn.execute("PRAGMA synchronous=NORMAL")
        self._create_tables()

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
            crawl_time      INTEGER
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
            "ALTER TABLE comments ADD COLUMN sort_order INTEGER DEFAULT 0",
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
            sort_order      INTEGER DEFAULT 0
        );
        CREATE TABLE IF NOT EXISTS config (
            key   TEXT PRIMARY KEY,
            value TEXT
        );
        CREATE INDEX IF NOT EXISTS idx_tweets_user_id ON tweets(user_id);
        CREATE INDEX IF NOT EXISTS idx_tweets_created_at ON tweets(created_at);
        CREATE INDEX IF NOT EXISTS idx_tweets_deleted ON tweets(deleted);
        CREATE INDEX IF NOT EXISTS idx_comments_tweet_id ON comments(tweet_id);
        """)
            self.conn.commit()

    def close(self):
        with self._lock:
            self.conn.close()

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()

    def insert_tweet(self, item):
        with self._lock:
            self.conn.execute("""
        INSERT INTO tweets
            (id, mblogid, content, user_id, created_at, reposts_count,
             comments_count, attitudes_count, pic_urls, pic_num,
             source, ip_location, is_retweet, retweet_id, deleted,
             deleted_at, url, crawl_time, screen_name, retweet_content, retweet_user, retweet_pic_urls, retweet_has_video)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(id) DO UPDATE SET
            content=excluded.content,
            reposts_count=excluded.reposts_count,
            comments_count=excluded.comments_count,
            attitudes_count=excluded.attitudes_count,
            retweet_content=excluded.retweet_content,
            retweet_user=excluded.retweet_user,
            retweet_pic_urls=excluded.retweet_pic_urls,
            crawl_time=excluded.crawl_time
        """, (
                item['_id'], item.get('mblogid'),
                item['content'], item.get('user_id', ''),
                item.get('created_at'),
                item.get('reposts_count', 0), item.get('comments_count', 0),
                item.get('attitudes_count', 0),
                json.dumps(item.get('pic_urls', [])) if isinstance(item.get('pic_urls'), list) else (item.get('pic_urls') or '[]'),
                item.get('pic_num', 0), item.get('source', ''),
                item.get('ip_location', ''), int(item.get('is_retweet', False)),
                item.get('retweet_id'), item.get('url', ''),
                item.get('crawl_time', int(time.time())),
                item.get('screen_name', ''),
                item.get('retweet_content', ''),
                item.get('retweet_user', ''),
                json.dumps(item.get('retweet_pic_urls', [])) if isinstance(item.get('retweet_pic_urls'), list) else (item.get('retweet_pic_urls') or '[]'),
                int(item.get('retweet_has_video', False)),
            ))
            self.conn.commit()

    def insert_comment(self, item):
        with self._lock:
            self.conn.execute("""
        INSERT OR REPLACE INTO comments
            (id, tweet_id, content, created_at, like_counts,
             ip_location, comment_user, reply_comment, crawl_time, parent_comment_id, sort_order)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """, (
                item['_id'], item['tweet_id'], item['content'],
                item.get('created_at'), item.get('like_counts', 0),
                item.get('ip_location', ''),
                json.dumps(item.get('comment_user', {})) if isinstance(item.get('comment_user'), dict) else (item.get('comment_user') or '{}'),
                json.dumps(item.get('reply_comment')) if isinstance(item.get('reply_comment'), dict) else (item.get('reply_comment')),
                item.get('crawl_time', int(time.time())),
                item.get('parent_comment_id'),
                item.get('sort_order', 0),
            ))
            self.conn.commit()

    def get_tweets(self, page=1, per_page=20, sort='desc', deleted='exclude', user_id=None):
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
            cur = self.conn.execute(
                f"UPDATE tweets SET deleted=1, deleted_at=? WHERE id IN ({placeholders})",
                [now] + list(ids)
            )
            self.conn.commit()
            return cur.rowcount

    def restore_tweets(self, ids):
        with self._lock:
            if not ids:
                return 0
            placeholders = ','.join(['?'] * len(ids))
            cur = self.conn.execute(
                f"UPDATE tweets SET deleted=0, deleted_at=NULL WHERE id IN ({placeholders})",
                list(ids)
            )
            self.conn.commit()
            return cur.rowcount

    def get_tweet_ids(self):
        with self._lock:
            rows = self.conn.execute("SELECT mblogid FROM tweets WHERE deleted=0").fetchall()
            return [r[0] for r in rows]

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
            self.conn.execute(
                "INSERT OR REPLACE INTO config (key, value) VALUES (?, ?)",
                (key, str(value))
            )
            self.conn.commit()
