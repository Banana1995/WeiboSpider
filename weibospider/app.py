# weibospider/app.py
import json
import uuid
import logging
import os
import subprocess
import sys
import threading
import time
import tempfile
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


def _get_user_ids():
    """Get user IDs from DB config, fallback to defaults."""
    ids = DB.get_config('user_ids')
    if ids:
        return ids
    return list(DEFAULT_USER_IDS)


def _safe_remove(path):
    try:
        os.remove(path)
    except OSError:
        pass


def _get_schedule_config():
    """Read schedule config from DB with proper type coercion."""
    se = DB.get_config('schedule_enabled', False)
    if isinstance(se, str):
        se = se.lower() == 'true'
    return {
        'schedule_enabled': se,
        'schedule_start_hour': int(DB.get_config('schedule_start_hour', 5)),
        'schedule_end_hour': int(DB.get_config('schedule_end_hour', 23)),
        'tweet_interval_minutes': int(DB.get_config('tweet_interval_minutes', 60)),
        'comment_interval_minutes': int(DB.get_config('comment_interval_minutes', 30)),
    }


def _make_log_helpers(tag, log_file, unbuffered_env):
    """Create _log and _run_scrapy_with_log helpers with a tag prefix."""
    def _log(msg):
        ts = datetime.now().strftime('%H:%M:%S')
        line = f"[{ts}] [{tag}] {msg}"
        logger.info(msg)
        try:
            with open(log_file, 'a') as lf:
                lf.write(line + '\n')
        except:
            pass

    def _run_scrapy_with_log(cmd_args):
        proc = subprocess.Popen(cmd_args, cwd=os.path.dirname(os.path.abspath(__file__)),
                                env=unbuffered_env,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        captured = []
        def _forward():
            for line in proc.stdout:
                stripped = line.rstrip('\n')
                if not stripped:
                    continue
                captured.append(stripped)
                print(stripped, file=sys.stderr, flush=True)
                try:
                    with open(log_file, 'a') as lf:
                        lf.write(stripped + '\n')
                except:
                    pass
        thread = threading.Thread(target=_forward, daemon=True)
        thread.start()
        return proc, thread, captured

    return _log, _run_scrapy_with_log


def _crawl_tweets(scheduler=None, mode='full', user_id=None):
    """Crawl tweets. mode='incremental' stops at existing tweets; 'full' crawls all."""
    script_dir = os.path.dirname(os.path.abspath(__file__))
    log_file = os.path.join(script_dir, 'crawl.log')
    all_ids = _get_user_ids()
    unbuffered_env = {**os.environ, 'PYTHONUNBUFFERED': '1'}

    _log, _run_scrapy_with_log = _make_log_helpers('tweet', log_file, unbuffered_env)

    if user_id:
        if user_id not in all_ids:
            return {'status': 'failed', 'error': f'UID {user_id} 不在配置列表中'}
        user_ids = [user_id]
    else:
        user_ids = all_ids

    if mode == 'full':
        try:
            open(log_file, 'w').close()
        except:
            pass

    _log(f"====== 开始抓取推文 (mode={mode}, 用户: {user_ids}) ======")

    def _check_cancel():
        return scheduler and scheduler.tweet_cancelled

    cookie = DB.get_config('cookie', '')
    if not cookie:
        _log("失败: 未配置 Cookie")
        return {'status': 'failed', 'error': '未配置 Cookie'}
    cookie_path = os.path.join(script_dir, 'cookie.txt')
    with open(cookie_path, 'w') as f:
        f.write(cookie.strip())

    start_time = DB.get_config('start_date', '')
    end_time = DB.get_config('end_date', '')
    if mode == 'full':
        if not start_time or not end_time:
            start_time = (datetime.now() - timedelta(days=7)).strftime('%Y-%m-%d')
            end_time = datetime.now().strftime('%Y-%m-%d')
        _log(f"时间范围: {start_time} ~ {end_time}")
    else:
        start_time = None
        end_time = None

    tweets_before = DB.conn.execute("SELECT COUNT(*) FROM tweets").fetchone()[0]
    _log(f"微博抓取前已有 {tweets_before} 条")

    for uid in user_ids:
        if _check_cancel():
            _log("用户取消了抓取")
            return {'status': 'cancelled'}

        _log(f"抓取用户 {uid} 的微博...")
        items_file = tempfile.NamedTemporaryFile(
            suffix='.json', mode='w', delete=False, dir=script_dir)
        items_path = items_file.name
        items_file.close()
        cmd = [
            sys.executable, '-m', 'scrapy', 'crawl', 'tweet_spider_by_user_id',
            '-a', 'user_ids=%s' % uid,
            '-s', 'LOG_LEVEL=INFO',
            '-o', items_path,
        ]
        if start_time and end_time:
            cmd.extend(['-a', 'start_time=%s' % start_time, '-a', 'end_time=%s' % end_time])
        if mode == 'incremental':
            stop_id = DB.get_latest_tweet_id(uid)
            if stop_id:
                cmd.extend(['-a', 'stop_after_id=%s' % stop_id])
                _log(f"增量模式: stop_after_id={stop_id}")

        proc, _t, captured = _run_scrapy_with_log(cmd)
        while proc.poll() is None:
            if _check_cancel():
                proc.kill()
                try: proc.wait(timeout=2)
                except subprocess.TimeoutExpired: pass
                _log("用户取消了抓取")
                _safe_remove(items_path)
                return {'status': 'cancelled'}
            time.sleep(0.5)

        if proc.returncode != 0:
            _log(f"失败! 微博抓取 returncode={proc.returncode}")
            _safe_remove(items_path)
            return {'status': 'failed', 'stage': 'tweets', 'user_id': uid,
                    'error': f'returncode={proc.returncode}'}

        spider_output = '\n'.join(captured)
        if 'ok=-100' in spider_output or 'not logged in' in spider_output.lower():
            _log("失败: Cookie 已过期")
            _safe_remove(items_path)
            return {'status': 'failed', 'error': 'Cookie 已过期，请更新 Cookie',
                    'stage': 'tweets', 'user_id': uid}

        try:
            with open(items_path, 'r', encoding='utf-8') as f:
                items = json.load(f)
        except (json.JSONDecodeError, OSError) as e:
            _log(f"失败: 读取 scrapy 输出出错 {e}")
            _safe_remove(items_path)
            return {'status': 'failed', 'stage': 'tweets', 'user_id': uid,
                    'error': f'items read failed: {e}'}

        for it in items:
            it['user_id'] = uid
            if 'user' in it:
                it['screen_name'] = it['user'].get('nick_name', '')
                del it['user']
            else:
                it['screen_name'] = it.get('screen_name', '')
        try:
            DB.batch_insert_tweets(items)
        except Exception as e:
            _log(f"失败: 写入数据库出错 {e}")
            _safe_remove(items_path)
            return {'status': 'failed', 'stage': 'tweets', 'user_id': uid,
                    'error': f'db insert failed: {e}'}

        _safe_remove(items_path)
        tweets_after = DB.conn.execute("SELECT COUNT(*) FROM tweets").fetchone()[0]
        new_tweets = tweets_after - tweets_before
        _log(f"用户 {uid} 微博抓取完成 (新增 {new_tweets} 条, 总计 {tweets_after} 条)")

    stats = DB.stats()
    return {'status': 'completed', 'stats': stats}


def _crawl_comments(scheduler=None, mode='full'):
    """Crawl comments. mode='incremental' uses 8h window + 2 pages; 'full' uses date range."""
    script_dir = os.path.dirname(os.path.abspath(__file__))
    log_file = os.path.join(script_dir, 'crawl.log')
    unbuffered_env = {**os.environ, 'PYTHONUNBUFFERED': '1'}

    _log, _run_scrapy_with_log = _make_log_helpers('comment', log_file, unbuffered_env)

    def _check_cancel():
        return scheduler and scheduler.comment_cancelled

    cookie = DB.get_config('cookie', '')
    if not cookie:
        _log("失败: 未配置 Cookie")
        return {'status': 'failed', 'error': '未配置 Cookie'}
    cookie_path = os.path.join(script_dir, 'cookie.txt')
    with open(cookie_path, 'w') as f:
        f.write(cookie.strip())

    if mode == 'incremental':
        tweet_pairs = DB.get_tweets_for_comment_crawl(hours=8)
        tweet_ids = [mblogid for _, mblogid in tweet_pairs]
        _log(f"增量模式: {len(tweet_ids)} 条微博待补齐评论 (8h 内, <100 评论)")
    else:
        start_time = DB.get_config('start_date', '')
        end_time = DB.get_config('end_date', '')
        if not start_time or not end_time:
            start_time = (datetime.now() - timedelta(days=7)).strftime('%Y-%m-%d')
            end_time = datetime.now().strftime('%Y-%m-%d')
        total_ids = DB.get_tweet_ids(start_date=start_time, end_date=end_time)
        skip_ids = DB.get_tweet_ids_with_enough_comments(100, start_date=start_time, end_date=end_time)
        tweet_ids = [tid for tid in total_ids if tid not in skip_ids]
        _log(f"全量模式: 共 {len(total_ids)} 条, 跳过 {len(skip_ids)} 条(≥100评论), 抓取 {len(tweet_ids)} 条")

    if not tweet_ids:
        _log("没有微博需要抓评论")
        stats = DB.stats()
        return {'status': 'completed', 'stats': stats}

    comments_before = DB.conn.execute("SELECT COUNT(*) FROM comments").fetchone()[0]
    _log(f"开始抓取评论 ({len(tweet_ids)} 条微博, 已有 {comments_before} 条评论)")

    items_file = tempfile.NamedTemporaryFile(
        suffix='.json', mode='w', delete=False, dir=script_dir)
    items_path = items_file.name
    items_file.close()
    cmd = [
        sys.executable, '-m', 'scrapy', 'crawl', 'comment',
        '-a', 'tweet_ids=%s' % ','.join(tweet_ids),
        '-a', 'flow=0',
        '-s', 'LOG_LEVEL=INFO',
        '-o', items_path,
    ]
    if mode == 'incremental':
        cmd.extend(['-a', 'max_pages=2'])

    proc, _t, captured = _run_scrapy_with_log(cmd)
    while proc.poll() is None:
        if _check_cancel():
            proc.kill()
            try: proc.wait(timeout=2)
            except subprocess.TimeoutExpired: pass
            _log("用户取消了抓取")
            _safe_remove(items_path)
            return {'status': 'cancelled'}
        time.sleep(1)

    spider_output = '\n'.join(captured)
    if 'ok=-100' in spider_output or 'not logged in' in spider_output.lower():
        _log("失败: Cookie 已过期")
        _safe_remove(items_path)
        return {'status': 'failed', 'error': 'Cookie 已过期，请更新 Cookie',
                'stats': DB.stats()}

    if proc.returncode != 0:
        _log(f"评论抓取失败! returncode={proc.returncode}")
        _safe_remove(items_path)
        return {'status': 'failed', 'error': f'returncode={proc.returncode}', 'stats': DB.stats()}

    try:
        with open(items_path, 'r', encoding='utf-8') as f:
            items = json.load(f)
    except (json.JSONDecodeError, OSError) as e:
        _log(f"失败: 读取 scrapy 输出出错 {e}")
        _safe_remove(items_path)
        return {'status': 'failed', 'error': f'items read failed: {e}', 'stats': DB.stats()}

    try:
        DB.batch_insert_comments(items)
    except Exception as e:
        _log(f"失败: 写入数据库出错 {e}")
        _safe_remove(items_path)
        return {'status': 'failed', 'error': f'db insert failed: {e}', 'stats': DB.stats()}

    _safe_remove(items_path)
    comments_after = DB.conn.execute("SELECT COUNT(*) FROM comments").fetchone()[0]
    new_comments = comments_after - comments_before

    _log(f"评论抓取完成 (新增 {new_comments} 条, 总计 {comments_after} 条)")
    stats = DB.stats()
    return {'status': 'completed', 'stats': stats}


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
    # Attach comments and annotations for each tweet
    for t in tweets:
        comments = DB.get_comments(t['id'], sort='hot')
        t['comments_list'] = comments
        t['annotations_list'] = DB.get_annotations(t['id'])
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
    DB.insert_log('tweet', 'delete', detail=f'tweet={tweet_id} count={count}',
                   status='success', user='web')
    return jsonify({'deleted': count})


@app.route('/api/tweets/batch-delete', methods=['DELETE'])
def api_batch_delete():
    data = request.get_json()
    if data is None:
        return jsonify({'error': 'invalid JSON'}), 400
    ids = data.get('ids', [])
    count = DB.batch_delete(ids)
    DB.insert_log('tweet', 'batch_delete', detail=f'ids={len(ids)} count={count}',
                   status='success', user='web')
    return jsonify({'deleted': count})


@app.route('/api/tweets/restore', methods=['POST'])
def api_restore():
    data = request.get_json()
    if data is None:
        return jsonify({'error': 'invalid JSON'}), 400
    ids = data.get('ids', [])
    count = DB.restore_tweets(ids)
    DB.insert_log('tweet', 'restore', detail=f'ids={len(ids)} count={count}',
                   status='success', user='web')
    return jsonify({'restored': count})


@app.route('/api/tweets/<tweet_id>/annotations')
def api_get_annotations(tweet_id):
    return jsonify(DB.get_annotations(tweet_id))


@app.route('/api/tweets/<tweet_id>/annotations', methods=['POST'])
def api_create_annotation(tweet_id):
    tweet = DB.get_tweet(tweet_id)
    if tweet is None:
        return jsonify({'error': 'tweet not found'}), 404
    data = request.get_json()
    if not data or 'selected_text' not in data:
        return jsonify({'error': 'missing selected_text'}), 400
    item = {
        'id': str(uuid.uuid4()),
        'tweet_id': tweet_id,
        'start_offset': data.get('start_offset', 0),
        'end_offset': data.get('end_offset', 0),
        'selected_text': data.get('selected_text', ''),
        'comment': data.get('comment', ''),
        'field': data.get('field', 'content'),
        'ranges': data.get('ranges'),
    }
    result = DB.insert_annotation(item)
    DB.insert_log('annotation', 'create',
                   detail=f'tweet={tweet_id} text={data.get("selected_text","")[:30]}',
                   status='success', user='web')
    return jsonify(result), 201


@app.route('/api/annotations/<ann_id>')
def api_get_annotation(ann_id):
    result = DB.get_annotation(ann_id)
    if result is None:
        return jsonify({'error': 'annotation not found'}), 404
    return jsonify(result)


@app.route('/api/annotations/<ann_id>', methods=['PUT'])
def api_update_annotation(ann_id):
    data = request.get_json()
    if not data or 'comment' not in data:
        return jsonify({'error': 'missing comment'}), 400
    result = DB.update_annotation(ann_id, data['comment'])
    if result is None:
        return jsonify({'error': 'annotation not found'}), 404
    DB.insert_log('annotation', 'update',
                   detail=f'ann={ann_id} comment={data["comment"][:30]}',
                   status='success', user='web')
    return jsonify(result)


@app.route('/api/annotations/<ann_id>', methods=['DELETE'])
def api_delete_annotation(ann_id):
    deleted = DB.delete_annotation(ann_id)
    if not deleted:
        return jsonify({'error': 'annotation not found'}), 404
    DB.insert_log('annotation', 'delete', detail=f'ann={ann_id}',
                   status='success', user='web')
    return jsonify({'deleted': True})


@app.route('/api/tweets/<tweet_id>/crawl-comments', methods=['POST'])
def api_crawl_comments(tweet_id):
    """Crawl hot-sorted comments for a single tweet on demand."""
    tweet = DB.get_tweet(tweet_id)
    if tweet is None:
        return jsonify({'error': 'tweet not found'}), 404
    mblogid = tweet.get('mblogid')
    if not mblogid:
        return jsonify({'error': 'tweet has no mblogid'}), 400

    script_dir = os.path.dirname(os.path.abspath(__file__))
    cookie = DB.get_config('cookie', '')
    if not cookie:
        return jsonify({'error': '未配置 Cookie'}), 400
    cookie_path = os.path.join(script_dir, 'cookie.txt')
    with open(cookie_path, 'w') as f:
        f.write(cookie.strip())

    comments_before = DB.conn.execute(
        "SELECT COUNT(*) FROM comments WHERE tweet_id=?", (tweet_id,)
    ).fetchone()[0]

    items_file = tempfile.NamedTemporaryFile(
        suffix='.json', mode='w', delete=False, dir=script_dir)
    items_path = items_file.name
    items_file.close()
    cmd = [
        sys.executable, '-m', 'scrapy', 'crawl', 'comment',
        '-a', 'tweet_ids=%s' % mblogid,
        '-a', 'flow=0',
        '-s', 'LOG_LEVEL=INFO',
        '-o', items_path,
    ]
    try:
        proc = subprocess.run(cmd, cwd=script_dir, capture_output=True, text=True, timeout=120)
    except subprocess.TimeoutExpired:
        _safe_remove(items_path)
        DB.insert_log('crawl', 'single_comment_crawl',
                       detail=f'tweet={tweet_id}', status='failed', user='web')
        return jsonify({'error': '抓取超时'}), 504

    spider_output = (proc.stdout or '') + (proc.stderr or '')
    if 'ok=-100' in spider_output or 'not logged in' in spider_output.lower():
        _safe_remove(items_path)
        DB.insert_log('crawl', 'single_comment_crawl',
                       detail=f'tweet={tweet_id}', status='failed', user='web')
        return jsonify({'error': 'Cookie 已过期，请更新 Cookie'}), 400

    if proc.returncode != 0:
        _safe_remove(items_path)
        tail = spider_output[-500:] if spider_output else ''
        DB.insert_log('crawl', 'single_comment_crawl',
                       detail=f'tweet={tweet_id} returncode={proc.returncode}',
                       status='failed', user='web')
        return jsonify({'error': f'scrapy failed (returncode={proc.returncode})', 'output': tail}), 500

    try:
        with open(items_path, 'r', encoding='utf-8') as f:
            items = json.load(f)
        DB.batch_insert_comments(items)
    except Exception as e:
        _safe_remove(items_path)
        DB.insert_log('crawl', 'single_comment_crawl',
                       detail=f'tweet={tweet_id} read/insert failed: {e}',
                       status='failed', user='web')
        return jsonify({'error': f'读取或写入数据失败: {e}'}), 500

    _safe_remove(items_path)
    comments = DB.get_comments(tweet_id, sort='hot')
    new_count = len(comments) - comments_before
    if new_count == 0 and len(comments) == 0:
        DB.insert_log('crawl', 'single_comment_crawl',
                       detail=f'tweet={tweet_id} no_comments', status='failed', user='web')
        return jsonify({'error': '未抓取到评论（可能 Cookie 过期或该微博无评论）', 'output': spider_output[-500:]}), 500

    DB.insert_log('crawl', 'single_comment_crawl',
                   detail=f'tweet={tweet_id} new={new_count} total={len(comments)}',
                   status='success', user='web')
    return jsonify({'count': len(comments), 'new_count': new_count, 'comments': comments})


@app.route('/api/crawl', methods=['POST'])
def api_crawl():
    if SCHEDULER is None:
        return jsonify({'status': 'started', 'message': 'Scheduler disabled (test mode)'})
    data = request.get_json(silent=True) or {}
    user_id = data.get('user_id')  # optional: crawl only one user
    DB.insert_log('crawl', 'manual_full', detail=f'user_id={user_id}', user='web')
    result = SCHEDULER.manual_crawl(user_id=user_id)
    return jsonify(result)


@app.route('/api/crawl/incremental', methods=['POST'])
def api_crawl_incremental():
    if SCHEDULER is None:
        return jsonify({'status': 'started', 'message': 'Scheduler disabled (test mode)'})
    DB.insert_log('crawl', 'manual_incremental', user='web')
    result = SCHEDULER.manual_incremental()
    return jsonify(result)


@app.route('/api/crawl/status')
def api_crawl_status():
    if SCHEDULER is None:
        return jsonify({
            'tweet': {'running': False, 'last_result': None},
            'comment': {'running': False, 'last_result': None},
        })
    return jsonify(SCHEDULER.status)


@app.route('/api/crawl/cancel', methods=['POST'])
def api_crawl_cancel():
    if SCHEDULER is None:
        return jsonify({'status': 'error', 'message': 'Scheduler not available'})
    DB.insert_log('crawl', 'cancel', user='web')
    return jsonify(SCHEDULER.cancel())


@app.route('/api/cookie/keepalive', methods=['POST'])
def api_cookie_keepalive():
    """Manually trigger cookie keepalive (ping weibo.com, refresh Set-Cookie)."""
    if SCHEDULER is None:
        return jsonify({'error': 'Scheduler not available'}), 503
    DB.insert_log('keepalive', 'manual_keepalive', user='web')
    result = SCHEDULER.manual_keepalive()
    code = 200 if result.get('status') == 'ok' else 400
    return jsonify(result), code


@app.route('/api/logs')
def api_logs():
    """Return paginated operation logs."""
    page = request.args.get('page', 1, type=int)
    per_page = request.args.get('per_page', 50, type=int)
    category = request.args.get('category') or None
    return jsonify(DB.get_logs(page=page, per_page=per_page, category=category))


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
                state = json.dumps(current, ensure_ascii=False, default=str)
                if state != last_state:
                    last_state = state
                    yield 'event: status\ndata: %s\n\n' % state

            # 2. Push new log lines while crawling or right after completion
            try:
                if os.path.exists(log_file):
                    file_size = os.path.getsize(log_file)
                    if last_log_pos > file_size:
                        last_log_pos = 0
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
    from keepalive import get_alf_expiry
    cookie = DB.get_config('cookie', '')
    masked = cookie[:20] + '...' + cookie[-10:] if len(cookie) > 30 else cookie
    config = _get_schedule_config()
    alf_ts = get_alf_expiry(cookie)
    cookie_expiry = None
    cookie_days_left = None
    if alf_ts:
        from datetime import datetime as _dt
        cookie_expiry = _dt.fromtimestamp(alf_ts).strftime('%Y-%m-%d %H:%M:%S')
        cookie_days_left = round((alf_ts - time.time()) / 86400, 1)
    return jsonify({
        'user_ids': _get_user_ids(),
        'cookie': cookie,
        'cookie_masked': masked,
        'cookie_expiry': cookie_expiry,
        'cookie_days_left': cookie_days_left,
        'start_date': DB.get_config('start_date', ''),
        'end_date': DB.get_config('end_date', ''),
        **config,
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
        DB.insert_log('config', 'set_cookie', status='success', user='web')
    if 'user_ids' in data:
        user_ids = data['user_ids']
        if isinstance(user_ids, str):
            user_ids = [uid.strip() for uid in user_ids.split(',') if uid.strip()]
        DB.set_config('user_ids', user_ids)
        updated['user_ids'] = user_ids
        DB.insert_log('config', 'set_user_ids',
                       detail=str(user_ids), status='success', user='web')
    if 'start_date' in data:
        DB.set_config('start_date', data['start_date'])
        updated['start_date'] = data['start_date']
    if 'end_date' in data:
        DB.set_config('end_date', data['end_date'])
        updated['end_date'] = data['end_date']
    schedule_keys = {
        'schedule_enabled': bool,
        'schedule_start_hour': int,
        'schedule_end_hour': int,
        'tweet_interval_minutes': int,
        'comment_interval_minutes': int,
    }
    schedule_changed = False
    for key, caster in schedule_keys.items():
        if key in data:
            val = caster(data[key])
            DB.set_config(key, str(val) if key == 'schedule_enabled' else val)
            updated[key] = val
            schedule_changed = True
    if schedule_changed and SCHEDULER:
        SCHEDULER.update_config(_get_schedule_config())
        DB.insert_log('schedule', 'config_update',
                       detail=str({k: updated.get(k) for k in schedule_keys if k in updated}),
                       status='success', user='web')
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
        DB.insert_log('export', 'pdf_html',
                       detail=f'range={start}~{end} tweets={len(results)}',
                       status='success', user='web')
        return _render_export_html(results, start, end)
    if format == 'pdf':
        DB.insert_log('export', 'pdf_export',
                       detail=f'range={start}~{end} tweets={len(results)}',
                       status='success', user='web')
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


def create_app(db_path=None, debug=False):
    global DB, SCHEDULER
    DB = TweetDB(db_path)
    # Seed default config values on first run
    if DB.get_config('cookie', None) is None:
        DB.set_config('cookie', '')
    if not DB.get_config('user_ids', None):
        DB.set_config('user_ids', DEFAULT_USER_IDS)
    # One-time migration: trash retweets of other users' content
    if not DB.get_config('retweet_trash_migrated'):
        DB.migrate_retweet_trash()
        DB.set_config('retweet_trash_migrated', True)
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
    # Start scheduler only in the actual serving process:
    # - Production (debug=False): start here
    # - Dev with reloader (debug=True): start ONLY in reloaded child (WERKZEUG_RUN_MAIN=='true')
    if not debug or os.environ.get('WERKZEUG_RUN_MAIN') == 'true':
        if SCHEDULER is None and not app.config.get('SCHEDULER_DISABLED'):
            from keepalive import refresh_cookie
            _keepalive = lambda: refresh_cookie(DB)
            SCHEDULER = CrawlScheduler(_crawl_tweets, _crawl_comments,
                                       keepalive_func=_keepalive)
            SCHEDULER._log_to_db = lambda cat, act, detail=None, status=None, user='scheduler': \
                DB.insert_log(cat, act, detail=detail, status=status, user=user)
            SCHEDULER._get_cookie = lambda: DB.get_config('cookie', '') or ''
            SCHEDULER._cleanup_old_logs = lambda days=15: DB.cleanup_old_logs(days)
            SCHEDULER.update_config(_get_schedule_config())
            SCHEDULER.start()
    return app
