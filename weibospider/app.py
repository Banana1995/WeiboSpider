# weibospider/app.py
import json
import logging
import os
import subprocess
import sys
import time
from datetime import datetime, timedelta

from flask import Flask, jsonify, request, send_from_directory, Response

from db import TweetDB
from scheduler import CrawlScheduler

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

DB = None
SCHEDULER = None
app = Flask(__name__, static_folder='static')


DEFAULT_USER_IDS = ['3962719063']
DEFAULT_COOKIE = 'SCF=Aj8cXU27CJwGzaF6zdheDmI8ep9CZwXBOCfIUaIZL5_PLAVK6yzR_sO-TCcJQWMTAZhLeUHNCcBb11jDMqnPqJc.; _qimei_uuid42=1a417140f0210085f7f57ea14ffce0f958c458c52d; _qimei_fingerprint=475a2000b231cf62d6774d3153452a9b; _qimei_i_3=46e05480930b0588c091af360fd674e8f1bbf2f5475351d7b2de205927962638303535973989e28290bc; _qimei_h38=; SUB=_2A25HGihtDeRhGeNN7FEX8CnJzzyIHXVkViWlrDV8PUNbmtANLVrgkW9NSYOsFhIXebUJWENxJOqxb2RiJJQyfguE; SUBP=0033WrSXqPxfM725Ws9jqgMF55529P9D9WFHEYOBNW25GVY89yMnAFVX5JpX5KzhUgL.Fo-0S0ecehMfSh52dJLoI0YLxK-L12qLBonLxK-LB.-L1KzLxKBLB.qL122LxKqL1KqL1hMLxKML1hnLBo2LxKML1KBLBo-LxK-LB.BLBo2t; ALF=02_1782965565; SINAGLOBAL=8401538047095.367.1780374151987; ULV=1780374151988:1:1:1:8401538047095.367.1780374151987:; _qimei_i_1=2cf92ce5eb22; XSRF-TOKEN=0Gd9B2Pu9tBiPve5qJGofwAT; WBPSESS=6AmhJwedr--y_2hMkuB9xuCCBASwYj5oPXwPbTy5roaVfOvjFstgGIFNrizxLGJ7Q4WJY8faXCw-ORg3mZoHEiCWuSbg7X6OkCzEeSFBYaQo_Ca40ZQnrfRp0mFIgfWyyoX68bOS9g8aSZrm40ltVQ=='


def _get_user_ids():
    """Get user IDs from DB config, fallback to defaults."""
    ids = DB.get_config('user_ids')
    if ids:
        return ids
    return list(DEFAULT_USER_IDS)


def _crawl(scheduler=None, user_id=None):
    """Execute crawl via subprocess. If user_id is given, only crawl that user."""
    script_dir = os.path.dirname(os.path.abspath(__file__))
    log_file = os.path.join(script_dir, 'crawl.log')
    log_lines = []
    all_ids = _get_user_ids()

    def _log(msg):
        ts = datetime.now().strftime('%H:%M:%S')
        line = f"[{ts}] {msg}"
        log_lines.append(line)
        logger.info(msg)
        try:
            with open(log_file, 'a') as lf:
                lf.write(line + '\n')
        except:
            pass

    if user_id:
        if user_id not in all_ids:
            return {'status': 'failed', 'error': f'UID {user_id} 不在配置列表中', 'log': ''}
        user_ids = [user_id]
    else:
        user_ids = all_ids

    _log(f"====== 开始抓取 (用户: {user_ids}) ======")

    def _check_cancel():
        return scheduler and scheduler.cancelled

    # Read cookie from DB, write to cookie.txt for Scrapy to pick up
    cookie = DB.get_config('cookie', '') or DEFAULT_COOKIE
    if not cookie:
        _log("失败: 未配置 Cookie")
        return {'status': 'failed', 'error': '未配置 Cookie', 'log': '\n'.join(log_lines)}
    cookie_path = os.path.join(script_dir, 'cookie.txt')
    with open(cookie_path, 'w') as f:
        f.write(cookie.strip())
    _log("Cookie 已写入 cookie.txt (len=%d)" % len(cookie))

    start_time = DB.get_config('start_date', '')
    end_time = DB.get_config('end_date', '')
    _log(f"时间范围: {start_time or '不限'} ~ {end_time or '不限'}")

    # Step 1: crawl tweets
    tweets_before = DB.conn.execute("SELECT COUNT(*) FROM tweets").fetchone()[0]
    _log(f"微博抓取前已有 {tweets_before} 条")

    for uid in user_ids:
        if _check_cancel():
            _log("用户取消了抓取")
            return {'status': 'cancelled', 'log': '\n'.join(log_lines)}

        _log(f"抓取用户 {uid} 的微博...")
        cmd = [
            sys.executable, '-m', 'scrapy', 'crawl', 'tweet_spider_by_user_id',
            '-a', 'user_ids=%s' % uid,
            '-s', 'ITEM_PIPELINES={"pipelines.SqlitePipeline": 300}',
        ]
        if start_time and end_time:
            cmd.extend(['-a', 'start_time=%s' % start_time, '-a', 'end_time=%s' % end_time])

        proc = subprocess.Popen(cmd, cwd=script_dir,
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        while proc.poll() is None:
            if _check_cancel():
                proc.kill(); proc.wait(timeout=2)
                _log("用户取消了抓取")
                return {'status': 'cancelled', 'log': '\n'.join(log_lines)}
            time.sleep(0.5)

        if proc.returncode != 0:
            _log(f"失败! 微博抓取 returncode={proc.returncode}")
            return {'status': 'failed', 'stage': 'tweets', 'user_id': uid,
                    'log': '\n'.join(log_lines), 'error': f'returncode={proc.returncode}'}

        tweets_after = DB.conn.execute("SELECT COUNT(*) FROM tweets").fetchone()[0]
        new_tweets = tweets_after - tweets_before
        _log(f"用户 {uid} 微博抓取完成 (新增 {new_tweets} 条, 总计 {tweets_after} 条)")

    # Step 2: crawl comments
    tweet_ids = DB.get_tweet_ids()
    if not tweet_ids:
        _log("没有微博需要抓评论")
        return {'tweets_total': 0, 'status': 'completed', 'log': '\n'.join(log_lines)}

    comments_before = DB.conn.execute("SELECT COUNT(*) FROM comments").fetchone()[0]
    _log(f"开始抓取评论 ({len(tweet_ids)} 条微博, 已有 {comments_before} 条评论)")

    all_ids_str = ','.join(tweet_ids)
    proc = subprocess.Popen([
        sys.executable, '-m', 'scrapy', 'crawl', 'comment',
        '-a', 'tweet_ids=%s' % all_ids_str,
        '-a', 'flow=0',
        '-s', 'ITEM_PIPELINES={"pipelines.SqlitePipeline": 300}',
    ], cwd=script_dir, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    while proc.poll() is None:
        if _check_cancel():
            proc.kill(); proc.wait(timeout=2)
            _log("用户取消了抓取")
            return {'status': 'cancelled', 'log': '\n'.join(log_lines)}
        time.sleep(1)

    comments_after = DB.conn.execute("SELECT COUNT(*) FROM comments").fetchone()[0]
    new_comments = comments_after - comments_before

    if proc.returncode != 0:
        _log(f"评论抓取失败! returncode={proc.returncode}")
    else:
        _log(f"评论抓取完成 (新增 {new_comments} 条, 总计 {comments_after} 条)")

    stats = DB.stats()
    _log(f"抓取结束: 微博 {stats['total_tweets']} 条, 评论 {stats['total_comments']} 条")
    return {'tweets_total': len(tweet_ids), 'status': 'completed', 'stats': stats,
            'log': '\n'.join(log_lines)}


@app.route('/')
def index():
    return send_from_directory('static', 'index.html')


@app.route('/api/tweets')
def api_tweets():
    page = request.args.get('page', 1, type=int)
    per_page = request.args.get('per_page', 20, type=int)
    sort = request.args.get('sort', 'desc')
    deleted = request.args.get('deleted', 'exclude')
    user_id = request.args.get('user_id')  # optional filter

    tweets = DB.get_tweets(page=page, per_page=per_page, sort=sort, deleted=deleted, user_id=user_id)
    # Attach comments for each tweet
    for t in tweets:
        comments = DB.get_comments(t['id'], sort='hot')
        t['comments_list'] = comments
    return jsonify(tweets)


@app.route('/api/tweets/<tweet_id>')
def api_get_tweet(tweet_id):
    tweet = DB.get_tweet(tweet_id)
    if tweet is None:
        return jsonify({'error': 'tweet not found'}), 404
    comments = DB.get_comments(tweet_id)
    return jsonify({'tweet': tweet, 'comments': comments})


@app.route('/api/tweets/<tweet_id>', methods=['DELETE'])
def api_delete_tweet(tweet_id):
    count = DB.batch_delete([tweet_id])
    return jsonify({'deleted': count})


@app.route('/api/tweets/batch-delete', methods=['DELETE'])
def api_batch_delete():
    data = request.get_json()
    if data is None:
        return jsonify({'error': 'invalid JSON'}), 400
    ids = data.get('ids', [])
    count = DB.batch_delete(ids)
    return jsonify({'deleted': count})


@app.route('/api/tweets/restore', methods=['POST'])
def api_restore():
    data = request.get_json()
    if data is None:
        return jsonify({'error': 'invalid JSON'}), 400
    ids = data.get('ids', [])
    count = DB.restore_tweets(ids)
    return jsonify({'restored': count})


@app.route('/api/crawl', methods=['POST'])
def api_crawl():
    if SCHEDULER is None:
        return jsonify({'status': 'started', 'message': 'Scheduler disabled (test mode)'})
    data = request.get_json(silent=True) or {}
    user_id = data.get('user_id')  # optional: crawl only one user
    result = SCHEDULER.manual_crawl(user_id=user_id)
    return jsonify(result)


@app.route('/api/crawl/status')
def api_crawl_status():
    if SCHEDULER is None:
        return jsonify({'running': False, 'last_result': None})
    return jsonify(SCHEDULER.status)


@app.route('/api/crawl/cancel', methods=['POST'])
def api_crawl_cancel():
    if SCHEDULER is None:
        return jsonify({'status': 'error', 'message': 'Scheduler not available'})
    return jsonify(SCHEDULER.cancel())


@app.route('/api/crawl/events')
def api_crawl_events():
    """SSE endpoint: pushes crawl status and real-time log lines to the client."""
    script_dir = os.path.dirname(os.path.abspath(__file__))
    log_file = os.path.join(script_dir, 'crawl.log')

    def generate():
        last_state = None
        last_log_pos = 0
        while True:
            # 1. Push status changes
            if SCHEDULER:
                current = SCHEDULER.status
                running = current.get('running', False)
                state = json.dumps(current, ensure_ascii=False, default=str)
                if state != last_state:
                    last_state = state
                    yield 'event: status\ndata: %s\n\n' % state

            # 2. Push new log lines while crawling or right after completion
            try:
                if os.path.exists(log_file):
                    with open(log_file, 'r') as f:
                        f.seek(last_log_pos)
                        new_lines = f.read()
                        if new_lines:
                            last_log_pos = f.tell()
                            for line in new_lines.strip().split('\n'):
                                if line.strip():
                                    yield 'event: log\ndata: %s\n\n' % line
            except Exception:
                pass

            time.sleep(1)
    return Response(generate(), mimetype='text/event-stream',
                    headers={'Cache-Control': 'no-cache', 'X-Accel-Buffering': 'no'})


@app.route('/api/config', methods=['GET'])
def api_get_config():
    cookie = DB.get_config('cookie', '') or DEFAULT_COOKIE
    masked = cookie[:20] + '...' + cookie[-10:] if len(cookie) > 30 else cookie
    return jsonify({
        'user_ids': _get_user_ids(),
        'cookie': cookie,
        'cookie_masked': masked,
        'start_date': DB.get_config('start_date', ''),
        'end_date': DB.get_config('end_date', ''),
    })


@app.route('/api/config', methods=['POST'])
def api_set_config():
    data = request.get_json()
    if not data:
        return jsonify({'error': 'invalid JSON'}), 400
    updated = {}
    if 'cookie' in data:
        DB.set_config('cookie', data['cookie'])
        updated['cookie'] = True
    if 'user_ids' in data:
        user_ids = data['user_ids']
        if isinstance(user_ids, str):
            user_ids = [uid.strip() for uid in user_ids.split(',') if uid.strip()]
        DB.set_config('user_ids', user_ids)
        updated['user_ids'] = user_ids
    if 'start_date' in data:
        DB.set_config('start_date', data['start_date'])
        updated['start_date'] = data['start_date']
    if 'end_date' in data:
        DB.set_config('end_date', data['end_date'])
        updated['end_date'] = data['end_date']
    if updated:
        return jsonify({'updated': True, **updated})
    return jsonify({'error': 'no valid field provided'}), 400


@app.route('/api/export')
def api_export():
    """Return all undeleted tweets with comments for PDF export, optional time range."""
    start = request.args.get('start', '')
    end = request.args.get('end', '')
    format = request.args.get('format', 'json')
    tweets = DB.get_tweets(page=1, per_page=10000, sort='desc', deleted='exclude')
    results = []
    for t in tweets:
        if start and t.get('created_at', '') < start:
            continue
        if end and t.get('created_at', '') > end + ' 23:59:59':
            continue
        t['comments_list'] = DB.get_comments(t['id'])
        results.append(t)

    if format == 'html':
        return _render_export_html(results, start, end)
    if format == 'pdf':
        return _render_export_pdf(results, start, end)
    return jsonify(results)


def _render_export_html(tweets, start, end):
    from html import escape
    range_text = f'{start} ~ {end}' if start and end else '全部'

    def _comment_user_name(c):
        cu = c.get('comment_user', {})
        if isinstance(cu, dict):
            return str(cu.get('nick_name', '用户'))
        return str(cu)

    cards = []
    for t in tweets:
        pics = t.get('pic_urls', []) or []
        retweet_pics = t.get('retweet_pic_urls', []) or []
        comments = t.get('comments_list', [])

        img_tags = ''.join('<img src="%s">' % escape(p) for p in pics)
        retweet_html = ''
        if t.get('retweet_content'):
            retweet_imgs = ''.join('<img src="%s">' % escape(p) for p in retweet_pics)
            retweet_html = (
                '<div class="retweet-block">'
                '<span class="retweet-user">@%s</span>: %s%s'
                '</div>'
            ) % (escape(str(t.get('retweet_user', ''))), escape(str(t.get('retweet_content', ''))), retweet_imgs)

        comment_html = ''
        if comments:
            comment_items = []
            for c in comments:
                comment_items.append(
                    '<div class="comment"><span class="comment-user">%s</span>: %s</div>'
                    % (escape(_comment_user_name(c)), escape(str(c.get('content', ''))))
                )
            comment_html = '<div class="comments">%s</div>' % ''.join(comment_items)

        card = (
            '<div class="card">'
            '<div class="card-meta">%s | %s | %s | %s</div>'
            '<div class="card-content">%s</div>%s%s'
            '<div class="stats">转发 %s | 评论 %s | 赞 %s</div>%s'
            '</div>'
        ) % (
            escape(str(t.get('screen_name', ''))),
            escape(str(t.get('created_at', ''))),
            escape(str(t.get('source', ''))),
            escape(str(t.get('ip_location', ''))),
            escape(str(t.get('content', ''))),
            img_tags,
            retweet_html,
            t.get('reposts_count', 0), t.get('comments_count', 0), t.get('attitudes_count', 0),
            comment_html,
        )
        cards.append(card)

    html = '''<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="UTF-8"><title>微博导出</title>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Noto+Sans+SC:wght@400;700&display=swap">
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:"Noto Sans SC","PingFang SC","Hiragino Sans GB","Microsoft YaHei",-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#fff;color:#333;padding:20px}
h1{font-size:18px;margin-bottom:4px}
.subtitle{font-size:12px;color:#888;margin-bottom:16px}
.card{border:1px solid #e0e0e0;border-radius:8px;padding:12px;margin-bottom:10px}
.card img{max-width:200px;max-height:200px;margin:4px;border-radius:4px;display:inline-block}
.card-meta{color:#999;font-size:11px;margin-bottom:6px}
.card-content{line-height:1.7;margin-bottom:8px;font-size:14px;color:#222}
.retweet-block{background:#f5f5f5;border-left:3px solid #ddd;padding:8px 10px;margin:6px 0;border-radius:4px;font-size:13px;color:#555}
.retweet-block img{max-width:150px;max-height:150px}
.retweet-user{color:#e67e22}
.comments{margin-top:8px;border-top:1px solid #eee;padding-top:6px}
.comment{font-size:12px;padding:3px 0;color:#444}
.comment-user{color:#e67e22}
.stats{display:flex;gap:16px;font-size:12px;color:#888;margin-top:6px}
@media print{body{padding:0}.no-print{display:none}}
</style></head><body>
<div class="no-print" style="margin-bottom:12px;">
  <button onclick="window.print()" style="padding:8px 20px;font-size:14px;cursor:pointer;background:#4a9eff;color:#fff;border:none;border-radius:6px;">打印 / 保存为 PDF</button>
  <span style="color:#888;font-size:12px;margin-left:8px;">或按 Ctrl+P (Mac: Cmd+P)</span>
</div>
<h1>微博导出</h1><div class="subtitle">范围: %s | 共 %d 条微博</div>
%s
</body></html>''' % (range_text, len(tweets), ''.join(cards))
    return html


def _render_export_pdf(tweets, start, end):
    """Generate PDF using headless Chrome with embedded CJK fonts."""
    import tempfile
    html_str = _render_export_html(tweets, start, end)
    # Write HTML to temp file for Chrome to render
    with tempfile.NamedTemporaryFile(suffix='.html', mode='w', encoding='utf-8', delete=False) as f:
        f.write(html_str)
        html_path = f.name
    pdf_path = html_path.replace('.html', '.pdf')
    chrome_path = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
    try:
        subprocess.run(
            [chrome_path, '--headless', '--disable-gpu', '--no-pdf-header-footer',
             f'--print-to-pdf={pdf_path}', '--virtual-time-budget=10000',
             f'file://{html_path}'],
            timeout=30, capture_output=True
        )
        with open(pdf_path, 'rb') as f:
            pdf_bytes = f.read()
    finally:
        for p in (html_path, pdf_path):
            try:
                os.unlink(p)
            except OSError:
                pass
    filename = f'weibo_export_{start}_{end}.pdf' if start and end else 'weibo_export.pdf'
    return Response(
        pdf_bytes,
        mimetype='application/pdf',
        headers={'Content-Disposition': f'attachment; filename="{filename}"'}
    )


@app.route('/api/stats')
def api_stats():
    return jsonify(DB.stats())


@app.errorhandler(404)
def not_found(e):
    return jsonify({'error': 'not found'}), 404


def create_app(db_path=None):
    global DB, SCHEDULER
    DB = TweetDB(db_path)
    # Seed default config values on first run
    if DB.get_config('cookie', None) is None:
        DB.set_config('cookie', DEFAULT_COOKIE)
    if not DB.get_config('user_ids', None):
        DB.set_config('user_ids', DEFAULT_USER_IDS)
    if DB.get_config('start_date', None) is None:
        two_days_ago = (datetime.now() - timedelta(days=2)).strftime('%Y-%m-%d')
        today = datetime.now().strftime('%Y-%m-%d')
        DB.set_config('start_date', two_days_ago)
        DB.set_config('end_date', today)

    # Graceful shutdown: checkpoint WAL and close DB
    import atexit
    def _cleanup():
        try:
            if DB:
                DB.conn.execute("PRAGMA wal_checkpoint(TRUNCATE)")
                DB.close()
        except:
            pass
    atexit.register(_cleanup)
    # Flask reloader support: only start scheduler in the reloaded child process
    if os.environ.get('WERKZEUG_RUN_MAIN') == 'true' or not app.debug:
        if SCHEDULER is None and not app.config.get('SCHEDULER_DISABLED'):
            SCHEDULER = CrawlScheduler(_crawl)
            SCHEDULER.start()
    return app
