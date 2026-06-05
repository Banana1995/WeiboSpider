# weibospider/app.py
import json
import logging
import os
import subprocess
import sys
import time

from flask import Flask, jsonify, request, send_from_directory

from db import TweetDB
from scheduler import CrawlScheduler

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

DB = None
SCHEDULER = None
app = Flask(__name__, static_folder='static')

# Configurable: the target user ID
USER_ID = os.environ.get('WEIBO_USER_ID', '1087770692')


def _crawl():
    """Execute crawl via subprocess to avoid Scrapy reactor restart issues."""
    script_dir = os.path.dirname(os.path.abspath(__file__))

    # Step 1: crawl tweets
    logger.info("Starting tweet crawl for user: %s", USER_ID)
    subprocess.run([
        sys.executable, '-m', 'scrapy', 'crawl', 'tweet_spider_by_user_id',
        '-a', 'user_ids=%s' % USER_ID,
        '-s', 'ITEM_PIPELINES={"pipelines.SqlitePipeline": 300}',
    ], cwd=script_dir, check=True)

    # Step 2: crawl comments for all tweets
    tweet_ids = DB.get_tweet_ids()
    logger.info("Crawling comments for %d tweets", len(tweet_ids))
    for mblogid in tweet_ids:
        subprocess.run([
            sys.executable, '-m', 'scrapy', 'crawl', 'comment',
            '-a', 'tweet_ids=%s' % mblogid,
            '-s', 'ITEM_PIPELINES={"pipelines.SqlitePipeline": 300}',
        ], cwd=script_dir, check=True)
        time.sleep(0.5)  # small delay between comment crawls

    return {
        'tweets_total': len(tweet_ids),
        'status': 'completed',
    }


@app.route('/')
def index():
    return send_from_directory('static', 'index.html')


@app.route('/api/tweets')
def api_tweets():
    page = request.args.get('page', 1, type=int)
    per_page = request.args.get('per_page', 20, type=int)
    sort = request.args.get('sort', 'desc')
    deleted = request.args.get('deleted', 'exclude')

    tweets = DB.get_tweets(page=page, per_page=per_page, sort=sort, deleted=deleted)
    # Attach comments for each tweet
    for t in tweets:
        comments = DB.get_comments(t['id'])
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
    result = SCHEDULER.manual_crawl()
    return jsonify(result)


@app.route('/api/crawl/status')
def api_crawl_status():
    if SCHEDULER is None:
        return jsonify({'running': False, 'last_result': None})
    return jsonify(SCHEDULER.status)


@app.route('/api/stats')
def api_stats():
    return jsonify(DB.stats())


@app.errorhandler(404)
def not_found(e):
    return jsonify({'error': 'not found'}), 404


def create_app(db_path=None):
    global DB, SCHEDULER
    DB = TweetDB(db_path)
    if SCHEDULER is None and not app.config.get('SCHEDULER_DISABLED'):
        SCHEDULER = CrawlScheduler(_crawl)
        SCHEDULER.start()
    return app
