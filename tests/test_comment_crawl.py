"""Regression coverage for a successful comment request that produces zero items."""
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

mode, output = sys.argv[2:]
class FixtureResponses:
    def process_request(self, request, spider):
        status = 200
        data = {'ok': 1, 'data': [], 'max_id': 0}
        if mode in ('items', 'pipeline', 'later_failure'):
            data['data'] = [{'id': 1, 'text_raw': 'fixture comment'}]
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
process.crawl(CommentSpider, tweet_ids='Mb1')
process.start()
'''

    def run(mode):
        if mode not in results:
            output = folder / (mode + '.json')
            # Match the API's pre-created NamedTemporaryFile, initially zero bytes.
            output.touch()
            env = dict(os.environ)
            env.pop('SCRAPY_SETTINGS_MODULE', None)
            proc = subprocess.run(
                [sys.executable, '-c', script, str(SPIDER_DIR), mode, str(output)],
                cwd=str(folder), env=env, capture_output=True, text=True, timeout=30,
            )
            assert proc.returncode == 0, proc.stderr
            results[mode] = (output, proc.stdout + proc.stderr)
        return results[mode]
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
    db.insert_tweet({'_id': '1', 'mblogid': 'Mb1', 'user_id': '123', 'content': 'fixture'})
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
    assert response.get_json() == {'count': 0, 'new_count': 0, 'comments': []}


def test_empty_result_keeps_existing_comments(client, monkeypatch, crawl_output):
    import app
    app.DB.insert_comment({'_id': 'c1', 'tweet_id': '1', 'content': 'stored'})
    mock_crawl(monkeypatch, crawl_output, 'empty')
    response = client.post('/api/tweets/1/crawl-comments')
    assert response.status_code == 200
    assert response.get_json()['new_count'] == 0
    assert response.get_json()['count'] == 1
    assert response.get_json()['comments'][0]['content'] == 'stored'


def test_button_reports_source_failure_instead_of_json_error(client, monkeypatch, crawl_output):
    mock_crawl(monkeypatch, crawl_output, 'html')
    response = client.post('/api/tweets/1/crawl-comments')
    assert response.status_code == 502
    assert response.get_json()['code'] == 'non_json'
    assert 'Expecting value' not in response.get_json()['error']


@pytest.mark.parametrize('mode,expected', [('empty', 'completed'), ('html', 'failed')])
def test_scheduled_comment_crawl_uses_same_outcome(client, monkeypatch, crawl_output, mode, expected):
    import app
    path, output = crawl_output(mode)
    monkeypatch.setattr(app.DB, 'get_tweets_for_comment_crawl', lambda **kwargs: [('1', 'Mb1')])

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
