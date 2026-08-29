"""Global search over tweets, comments and annotations.

Two query paths (see docs/superpowers/specs/2026-08-29-global-search-design.md):
  - keyword length >= 3  -> FTS5 MATCH on search_index (uses trigram index)
  - keyword length  < 3  -> LIKE over source tables (faster than LIKE on FTS5)
"""

import html

# FTS5 MATCH needs >= 3 chars with the trigram tokenizer; shorter keywords
# return zero rows, so they take the LIKE path instead.
MIN_MATCH_LEN = 3


def fts5_quote(s):
    """Quote a user keyword as an FTS5 phrase literal.

    Raw user input is NOT a safe FTS5 query: characters like ' " * : -
    and bare AND/OR/NOT raise OperationalError. Wrapping in double quotes
    turns the whole thing into a phrase, and inner quotes are doubled.
    """
    return '"' + (s or '').replace('"', '""') + '"'


def build_search_sql(q, page=1, per_page=20, source_type='all',
                     start_date=None, end_date=None):
    """Build (sql, params) for a search query.

    Returns rows shaped: doc_id, source_type, tweet_id, matched_text, highlight,
                         id, content, created_at, user_id, screen_name, platform
    `highlight` is filled by snippet() on the MATCH path and is NULL on the
    LIKE path (the caller highlights in Python via make_highlight()).
    `matched_text` is the concatenated source text the LIKE path searched
    (tweet content+retweet, comment content, or annotation comment+selected
    text), used so highlight shows the actual matched source.
    """
    page = max(int(page or 1), 1)
    per_page = min(max(int(per_page or 20), 1), 100)
    offset = (page - 1) * per_page

    filters = []
    filter_params = []
    if source_type and source_type != 'all':
        filters.append("s.source_type = ?")
        filter_params.append(source_type)
    if start_date:
        filters.append("t.created_at >= ?")
        filter_params.append(start_date)
    if end_date:
        filters.append("t.created_at <= ?")
        filter_params.append(end_date + ' 23:59:59')
    extra = (' AND ' + ' AND '.join(filters)) if filters else ''

    if len(q) >= MIN_MATCH_LEN:
        # Path A: FTS5 MATCH. Left operand MUST be the table name.
        # snippet() column index 3 == the `text` column.
        sql = f"""
        SELECT s.doc_id, s.source_type, s.tweet_id,
               snippet(search_index, 3, '<mark>', '</mark>', '…', 12) AS highlight,
               t.id, t.content, t.created_at, t.user_id, t.screen_name, t.platform
          FROM search_index s
          JOIN tweets t ON t.id = s.tweet_id
         WHERE search_index MATCH ?
           AND t.deleted = 0
           {extra}
         ORDER BY t.created_at DESC
         LIMIT ? OFFSET ?
        """
        params = [fts5_quote(q)] + filter_params + [per_page, offset]
        return sql, params

    # Path B: short keyword -> LIKE over source tables (never on the FTS5 table).
    like = f'%{q}%'
    sql = f"""
        SELECT s.doc_id, s.source_type, s.tweet_id, s.matched_text,
               NULL AS highlight,
               t.id, t.content, t.created_at, t.user_id, t.screen_name, t.platform
          FROM (
                SELECT id AS doc_id, 'tweet' AS source_type, id AS tweet_id,
                       COALESCE(content,'') || ' ' || COALESCE(retweet_content,'') AS matched_text
                  FROM tweets
                 WHERE content LIKE ? OR retweet_content LIKE ?
                UNION ALL
                SELECT id, 'comment', tweet_id, COALESCE(content,'')
                  FROM comments
                 WHERE content LIKE ?
                UNION ALL
                SELECT id, 'annotation', tweet_id,
                       COALESCE(comment,'') || ' ' || COALESCE(selected_text,'')
                  FROM annotations
                 WHERE comment LIKE ? OR selected_text LIKE ?
               ) s
          JOIN tweets t ON t.id = s.tweet_id
         WHERE t.deleted = 0
           {extra}
         ORDER BY t.created_at DESC
         LIMIT ? OFFSET ?
    """
    params = [like] * 5 + filter_params + [per_page, offset]
    return sql, params


def make_highlight(text, q, context=20):
    """Return an HTML-safe snippet of `text` with `q` wrapped in <mark>.

    Used by the LIKE path, which has no snippet(). Escapes first so that
    tweet content containing markup cannot inject HTML.
    """
    text = text or ''
    if not text:
        return ''
    if not q:
        return html.escape(text[:context * 4])

    idx = text.lower().find(q.lower())
    if idx < 0:
        return html.escape(text[:context * 4])

    start = max(0, idx - context)
    end = min(len(text), idx + len(q) + context)
    before = html.escape(text[start:idx])
    hit = html.escape(text[idx:idx + len(q)])
    after = html.escape(text[idx + len(q):end])
    out = f'{before}<mark>{hit}</mark>{after}'
    if start > 0:
        out = '…' + out
    if end < len(text):
        out = out + '…'
    return out


def escape_snippet(hl):
    """Escape snippet() output while preserving its <mark> markers.

    SQLite's snippet() does NOT HTML-escape the surrounding text, so a tweet
    containing `<script>` would pass it through raw. Escape everything, then
    restore the markers snippet() inserted.

    Cosmetic limit: a literal `<mark>` that was already part of the original
    content is indistinguishable from the markers snippet() inserted, so it
    gets restored as a real tag too. Cosmetic only - it cannot execute JS.
    """
    if not hl:
        return hl
    return html.escape(hl).replace('&lt;mark&gt;', '<mark>').replace('&lt;/mark&gt;', '</mark>')
