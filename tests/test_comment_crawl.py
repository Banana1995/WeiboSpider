"""Offline regression coverage for comment pagination, outcomes and stored counts."""
import json
import os
from pathlib import Path
import subprocess
import sys
from types import SimpleNamespace

import pytest

SPIDER_DIR = Path(__file__).resolve().parents[1] / 'weibospider'
sys.path.insert(0, str(SPIDER_DIR))


@pytest.fixture(scope='module')
def crawl_output(tmp_path_factory):
    """Use real Scrapy export/signals, with all HTTP responses supplied in memory."""
    folder = tmp_path_factory.mktemp('comment-feeds')
    results = {}
    script = '''
import json, sys
sys.path.insert(0, sys.argv[1])
from scrapy.crawler import CrawlerProcess
from scrapy.http import TextResponse
from spiders.comment import CommentSpider

mode, output, max_pages = sys.argv[2:]
class FixtureResponses:
    def process_request(self, request, spider):
        status = 200
        data = {'ok': 1, 'data': [], 'max_id': 0}
        if mode in ('items', 'pipeline', 'later_failure'):
            data['data'] = [{'id': 1, 'text_raw': 'fixture comment'}]
        if mode in ('paged', 'many'):
            page = request.meta['page_num']
            total = 57 if mode == 'paged' else 125
            start = (page - 1) * 20 + 1
            end = min(start + 19, total)
            data['data'] = [{'id': i, 'text_raw': 'fixture comment ' + str(i)}
                            for i in range(start, end + 1)]
            if page in (2, 3):
                data['data'][0]['comments'] = [{'id': 1000 + page, 'text_raw': 'reply'}]
            data['max_id'] = page if end < total else 0
        if mode == 'later_failure':
            data['max_id'] = 42
            if 'max_id=' in request.url: status = 403
        if mode == 'expired': data = {'ok': -100}
        if mode == 'rejected': data = {'ok': 0, 'msg': 'fixture rejection'}
        if mode == 'shape': data = {'ok': 1, 'data': None}
        if mode == 'callback': data['data'] = [None]
        if mode == 'http': status = 403
        body = '<html>fixture challenge</html>' if mode == 'html' else json.dumps(data)
        return TextResponse(request.url, status=status, body=body.encode(), encoding='utf-8', request=request)

class BrokenPipeline:
    def process_item(self, item, spider):
        raise RuntimeError('fixture database error')

process = CrawlerProcess(settings={
    'DOWNLOADER_MIDDLEWARES': {'__main__.FixtureResponses': 50},
    'ITEM_PIPELINES': {'__main__.BrokenPipeline': 100} if mode == 'pipeline' else {},
    'FEEDS': {output: {'format': 'json'}},
    'TELNETCONSOLE_ENABLED': False,
    'LOG_LEVEL': 'INFO',
})
process.settings.set('DOWNLOAD_DELAY', 0, priority='cmdline')
process.crawl(CommentSpider, tweet_ids='1', max_pages=max_pages or None)
process.start()
'''

    def run(mode, max_pages=None):
        key = (mode, max_pages)
        if key not in results:
            output = folder / (mode + '-' + str(max_pages) + '.json')
            # Match the API's pre-created NamedTemporaryFile, initially zero bytes.
            output.touch()
            env = dict(os.environ)
            env.pop('SCRAPY_SETTINGS_MODULE', None)
            proc = subprocess.run(
                [sys.executable, '-c', script, str(SPIDER_DIR), mode, str(output), str(max_pages or '')],
                cwd=str(folder), env=env, capture_output=True, text=True, timeout=30,
            )
            assert proc.returncode == 0, proc.stderr
            results[key] = (output, proc.stdout + proc.stderr)
        return results[key]
    return run


def test_real_empty_feed_is_valid_json_and_success(crawl_output):
    import app
    path, output = crawl_output('empty')
    assert json.loads(path.read_text()) == []
    assert app._read_comment_items(path, output) == []


def test_real_comments_still_export(crawl_output):
    import app
    path, output = crawl_output('items')
    items = app._read_comment_items(path, output)
    assert len(items) == 1
    assert items[0]['content'] == 'fixture comment'


@pytest.mark.parametrize('max_pages', [None, 3])
def test_twenty_item_pages_continue_until_exhausted(crawl_output, max_pages):
    import app
    path, output = crawl_output('paged', max_pages)
    items = app._read_comment_items(path, output)
    assert len(items) == 59
    assert sum(not item.get('parent_comment_id') for item in items) == 57
    assert '59 comments total (57 top + 2 sub), pages=3, stop=exhausted' in output


def test_page_budget_is_reported_as_truncated(crawl_output):
    import app
    path, output = crawl_output('paged', 2)
    with pytest.raises(app._CommentCrawlError) as error:
        app._read_comment_items(path, output)
    assert error.value.code == 'page_limit'
    assert 'pages=2, stop=page_limit' in output


def test_top_level_limit_keeps_replies_and_is_not_page_truncation(crawl_output):
    import app
    path, output = crawl_output('many', 5)
    items = app._read_comment_items(path, output)
    assert {item['_id'] for item in items if not item.get('parent_comment_id')} == set(range(1, 101))
    assert len(items) == 102
    assert '102 comments total (100 top + 2 sub), pages=5, stop=top_limit' in output


@pytest.mark.parametrize('mode,code', [
    ('html', 'non_json'),
    ('expired', 'cookie_expired'),
    ('rejected', 'api_error'),
    ('shape', 'invalid_response'),
    ('http', 'request_failed'),
    ('callback', 'processing_error'),
    ('pipeline', 'processing_error'),
    ('later_failure', 'request_failed'),
])
def test_scrapy_exit_zero_does_not_hide_failure(crawl_output, mode, code):
    import app
    path, output = crawl_output(mode)
    with pytest.raises(app._CommentCrawlError) as error:
        app._read_comment_items(path, output)
    assert error.value.code == code


@pytest.mark.parametrize('contents', ['', '{', '{}', '[{}]'])
def test_bad_export_is_not_a_successful_empty_result(tmp_path, contents):
    import app
    path = tmp_path / 'feed.json'
    path.write_text(contents)
    result = 'COMMENT_CRAWL_RESULT ' + json.dumps({'status': 'completed', 'item_count': 0})
    with pytest.raises(app._CommentCrawlError) as error:
        app._read_comment_items(path, result)
    assert error.value.code == 'invalid_output'


def test_missing_outcome_does_not_turn_failure_into_empty_success(tmp_path):
    import app
    path = tmp_path / 'feed.json'
    path.write_text('[]')
    with pytest.raises(app._CommentCrawlError) as error:
        app._read_comment_items(path, 'Spider closed (finished)')
    assert error.value.code == 'incomplete'


@pytest.fixture
def client(tmp_path, monkeypatch):
    import app
    from db import TweetDB
    db = TweetDB(str(tmp_path / 'test.db'))
    monkeypatch.setattr(app, 'DB', db)
    monkeypatch.setattr(app, 'SCHEDULER', None)
    # Redirect cookie/output files into the fixture directory, never the user's files.
    monkeypatch.setattr(app, '__file__', str(tmp_path / 'app.py'))
    monkeypatch.setitem(app.app.config, 'TESTING', True)
    db.set_config('cookie', 'SUB=fixture-not-a-real-session')
    db.insert_tweet({'_id': '1', 'mblogid': '1', 'user_id': '123', 'content': 'fixture',
                     'comments_count': 999})
    with app.app.test_client() as client:
        yield client
    db.close()


def mock_crawl(monkeypatch, crawl_output, mode):
    import app
    path, output = crawl_output(mode)

    def run(cmd, **kwargs):
        Path(cmd[cmd.index('-o') + 1]).write_text(path.read_text())
        return SimpleNamespace(returncode=0, stdout='', stderr=output)
    monkeypatch.setattr(app.subprocess, 'run', run)


def test_button_returns_success_for_no_comments(client, monkeypatch, crawl_output):
    mock_crawl(monkeypatch, crawl_output, 'empty')
    response = client.post('/api/tweets/1/crawl-comments')
    assert response.status_code == 200
    assert response.get_json() == {
        'count': 0, 'new_count': 0, 'comments': [],
        'comment_counts': {'total': 0, 'top_level': 0, 'replies': 0},
    }


def test_empty_result_keeps_existing_comments(client, monkeypatch, crawl_output):
    import app
    app.DB.insert_comment({'_id': 'c1', 'tweet_id': '1', 'content': 'stored'})
    app.DB.insert_comment({'_id': 'c2', 'tweet_id': '1', 'content': 'reply', 'parent_comment_id': 'c1'})
    mock_crawl(monkeypatch, crawl_output, 'empty')
    response = client.post('/api/tweets/1/crawl-comments')
    assert response.status_code == 200
    assert response.get_json()['new_count'] == 0
    assert response.get_json()['count'] == 1
    assert response.get_json()['comments'][0]['content'] == 'stored'
    assert response.get_json()['comment_counts'] == {'total': 2, 'top_level': 1, 'replies': 1}


def test_manual_new_count_includes_replies_and_ignores_duplicates(client, monkeypatch, crawl_output):
    import app
    path, output = crawl_output('paged')
    items = app._read_comment_items(path, output)
    app.DB.batch_insert_comments(items[:44])

    def run(cmd, **kwargs):
        # The production pipeline writes before the subprocess returns to Flask.
        app.DB.batch_insert_comments(items)
        Path(cmd[cmd.index('-o') + 1]).write_text(path.read_text())
        return SimpleNamespace(returncode=0, stdout='', stderr=output)

    monkeypatch.setattr(app.subprocess, 'run', run)
    response = client.post('/api/tweets/1/crawl-comments')
    assert response.status_code == 200
    data = response.get_json()
    assert data['new_count'] == 15
    assert data['count'] == 57
    expected = {'total': 59, 'top_level': 57, 'replies': 2}
    assert data['comment_counts'] == expected
    assert client.post('/api/tweets/1/crawl-comments').get_json()['new_count'] == 0
    assert client.get('/api/tweets/1').get_json()['comment_counts'] == expected
    card = client.get('/api/tweets').get_json()['tweets'][0]
    assert card['comments_count'] == 999  # source metadata is not the stored count
    assert card['comment_counts'] == expected


def test_expanding_comments_keeps_heat_order(client):
    import app
    app.DB.batch_insert_comments([
        {'_id': 'late', 'tweet_id': '1', 'content': 'newer but colder',
         'created_at': '2026-10-22 11:00:00', 'like_counts': 0, 'sort_order': 1},
        {'_id': 'early', 'tweet_id': '1', 'content': 'older but hotter',
         'created_at': '2026-10-22 08:00:00', 'like_counts': 50, 'sort_order': 0},
    ])
    data = client.get('/api/tweets/1').get_json()
    assert [c['id'] for c in data['comments']] == ['early', 'late']


def test_button_reports_source_failure_instead_of_json_error(client, monkeypatch, crawl_output):
    mock_crawl(monkeypatch, crawl_output, 'html')
    response = client.post('/api/tweets/1/crawl-comments')
    assert response.status_code == 502
    assert response.get_json()['code'] == 'non_json'
    assert 'Expecting value' not in response.get_json()['error']


def test_xueqiu_shared_button_counts_new_ids_across_replacement(client, monkeypatch):
    import app
    app.DB.set_config('xq_cookie', 'fixture')
    app.DB.insert_tweet({'_id': 'xq1', 'content': 'fixture', 'platform': 'xueqiu'})
    app.DB.batch_insert_comments([
        {'_id': cid, 'tweet_id': 'xq1', 'content': 'stored',
         'parent_comment_id': parent, 'platform': 'xueqiu'}
        for cid, parent in [('xc1', None), ('xc2', 'xc1'), ('xc99', None)]
    ])
    source = [
        {'id': 1, 'text': 'parent', 'user': {'screen_name': 'fixture'}, 'child_comments': [
            {'id': 2, 'text': 'old reply', 'user': {'screen_name': 'fixture'}},
            {'id': 3, 'text': 'new reply', 'user': {'screen_name': 'fixture'}},
        ]},
        {'id': 4, 'text': 'new parent', 'user': {'screen_name': 'fixture'}},
    ]
    monkeypatch.setattr(app, '_fetch_xueqiu_comments_v3', lambda *args, **kwargs: (source, 1))
    response = client.post('/api/tweets/xq1/crawl-comments')
    assert response.status_code == 200
    data = response.get_json()
    assert data['new_count'] == 2  # IDs 3 and 4, not the net row-count difference of 1
    assert data['comment_counts'] == {'total': 4, 'top_level': 2, 'replies': 2}
    assert app.DB.get_comment_ids('xq1') == {'xc1', 'xc2', 'xc3', 'xc4'}
    assert client.post('/api/tweets/xq1/crawl-comments').get_json()['new_count'] == 0


@pytest.mark.parametrize('mode,expected', [('empty', 'completed'), ('html', 'failed')])
def test_scheduled_comment_crawl_uses_same_outcome(client, monkeypatch, crawl_output, mode, expected):
    import app
    path, output = crawl_output(mode)
    monkeypatch.setattr(app.DB, 'get_tweets_for_comment_crawl', lambda **kwargs: [('1', '1')])

    def helpers(*args):
        def run(cmd):
            Path(cmd[cmd.index('-o') + 1]).write_text(path.read_text())
            return (SimpleNamespace(poll=lambda: 0, returncode=0),
                    SimpleNamespace(join=lambda **kwargs: None), output.splitlines())
        return lambda *args: None, run
    monkeypatch.setattr(app, '_make_log_helpers', helpers)
    result = app._crawl_comments(mode='incremental')
    assert result['status'] == expected
    if expected == 'failed':
        assert result['code'] == 'non_json'


def test_scheduled_entry_point_fetches_beyond_two_pages(client, monkeypatch, crawl_output):
    import app
    monkeypatch.setattr(app.DB, 'get_tweets_for_comment_crawl', lambda **kwargs: [('1', '1')])

    def helpers(*args):
        def run(cmd):
            # Run the real spider with the entry point's page budget, if any.
            max_pages = next((int(arg.split('=')[1]) for arg in cmd if arg.startswith('max_pages=')), None)
            path, output = crawl_output('paged', max_pages)
            Path(cmd[cmd.index('-o') + 1]).write_text(path.read_text())
            return (SimpleNamespace(poll=lambda: 0, returncode=0),
                    SimpleNamespace(join=lambda **kwargs: None), output.splitlines())
        return lambda *args: None, run

    monkeypatch.setattr(app, '_make_log_helpers', helpers)
    assert app._crawl_comments(mode='incremental')['status'] == 'completed'
    assert app.DB.get_comment_counts(['1'])['1'] == {'total': 59, 'top_level': 57, 'replies': 2}
