from pathlib import Path


INDEX_HTML = (
    Path(__file__).resolve().parents[1] / 'weibospider' / 'static' / 'index.html'
).read_text(encoding='utf-8')


def test_annotation_input_focus_is_deferred_until_mouseup_finishes():
    assert "sel.removeAllRanges();\n  showAnnotationInput(" in INDEX_HTML
    assert "requestAnimationFrame(() => textarea.focus());" in INDEX_HTML


def test_shift_enter_saves_annotation_while_enter_keeps_default_newline():
    assert "textarea.addEventListener('keydown', e => {" in INDEX_HTML
    assert "if (e.key === 'Enter' && e.shiftKey)" in INDEX_HTML
    assert "e.preventDefault();\n      saveAnnotation();" in INDEX_HTML


def test_incremental_sync_button_exists():
    assert 'id="btn-incremental"' in INDEX_HTML
    assert '增量同步' in INDEX_HTML


def test_schedule_config_inputs_exist():
    assert 'id="schedule-enabled"' in INDEX_HTML
    assert 'id="schedule-start-hour"' in INDEX_HTML
    assert 'id="schedule-end-hour"' in INDEX_HTML
    assert 'id="tweet-interval-minutes"' in INDEX_HTML
    assert 'id="comment-interval-minutes"' in INDEX_HTML


def test_trigger_incremental_function_exists():
    assert "async function triggerIncremental()" in INDEX_HTML
    assert "fetch('/api/crawl/incremental'" in INDEX_HTML


def test_sse_handles_dual_status():
    assert "data.tweet" in INDEX_HTML
    assert "data.comment" in INDEX_HTML


def test_tweet_row_uses_relative_positioning():
    assert '.tweet-row { position: relative; }' in INDEX_HTML


def test_tweet_card_has_no_max_width():
    assert 'max-width: 680px' not in INDEX_HTML


def test_annotation_panel_absolute_positioned():
    assert 'position: absolute' in INDEX_HTML
    assert 'left: 100%' in INDEX_HTML


def test_annotation_panel_max_height():
    assert 'max-height: 400px' in INDEX_HTML


def test_empty_annotation_panel_hidden():
    assert '.annotation-panel:empty { display: none; }' in INDEX_HTML
