from pathlib import Path


INDEX_HTML = (
    Path(__file__).resolve().parents[1] / 'weibospider' / 'static' / 'index.html'
).read_text(encoding='utf-8')


def test_annotation_input_focus_is_deferred_until_mouseup_finishes():
    assert "sel.removeAllRanges();" in INDEX_HTML
    assert "showAnnotationInput(" in INDEX_HTML
    assert "requestAnimationFrame(() => textarea.focus());" in INDEX_HTML


def test_shift_enter_saves_annotation_while_enter_keeps_default_newline():
    assert "textarea.addEventListener('keydown', e => {" in INDEX_HTML
    assert "if (e.key === 'Enter' && e.shiftKey)" in INDEX_HTML
    assert "e.preventDefault();\n      saveAnnotation();" in INDEX_HTML


def test_incremental_sync_button_exists():
    assert 'id="btn-incremental"' in INDEX_HTML
    assert '增量同步' in INDEX_HTML


def test_ps_tab_exists():
    assert 'data-tab="ps"' in INDEX_HTML
    assert 'PS图' in INDEX_HTML
    assert 'ps-mode' in INDEX_HTML


def test_load_ps_function_exists():
    assert "async function loadPs()" in INDEX_HTML
    assert "fetch('/api/ps')" in INDEX_HTML
    assert "loadPs();" in INDEX_HTML


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


def test_narrow_screen_hides_panel_by_default():
    assert '@media (max-width: 1399px)' in INDEX_HTML
    assert '.annotation-panel { display: none; }' in INDEX_HTML


def test_popover_open_class_exists():
    assert '.annotation-panel.popover-open' in INDEX_HTML
    assert 'z-index: 100' in INDEX_HTML


def test_highlight_click_handles_narrow_screen():
    assert "function isNarrowScreen()" in INDEX_HTML
    assert "matchMedia('(max-width: 1399px)')" in INDEX_HTML


def test_toggle_popover_function_exists():
    assert "function togglePopover(tweetId, annId)" in INDEX_HTML


def test_close_all_popovers_function_exists():
    assert "function closeAllPopovers()" in INDEX_HTML


def test_clear_popover_state_on_resize():
    assert "addEventListener('resize'" in INDEX_HTML


def test_popover_tracks_annotation_id_for_switching():
    assert 'panel.dataset.popoverAnnId === annId' in INDEX_HTML
    assert 'delete panel.dataset.popoverAnnId' in INDEX_HTML


def test_adjust_row_height_prevents_overlap():
    assert 'function adjustRowHeight(tweetId)' in INDEX_HTML
    assert 'row.style.minHeight' in INDEX_HTML


def test_adjust_row_height_called_after_render():
    assert 'adjustRowHeight(tweetId);' in INDEX_HTML


def test_resize_clears_min_height_on_narrow():
    assert "row.style.minHeight = ''" in INDEX_HTML


def test_get_offset_uses_tree_walker_not_range():
    assert 'document.createTreeWalker(fieldEl, NodeFilter.SHOW_TEXT)' in INDEX_HTML
    assert 'range.toString().length' not in INDEX_HTML


def test_handle_selection_accounts_for_leading_whitespace():
    assert 'leadingTrim' in INDEX_HTML
    assert 'trailingTrim' in INDEX_HTML
    assert 'trimStart()' in INDEX_HTML
    assert 'trimEnd()' in INDEX_HTML


def test_pending_annotation_highlights_before_showing_input():
    create_start = INDEX_HTML.index('async function createPendingAnnotation(')
    create_end = INDEX_HTML.index('\nfunction findFieldElement(', create_start)
    create_body = INDEX_HTML[create_start:create_end]

    assert "await loadAnnotationHighlights(tweetId);" in create_body
    assert create_body.index("await loadAnnotationHighlights(tweetId);") < create_body.index("showAnnotationInput(")


def test_pending_annotation_creation_does_not_reload_panel():
    create_start = INDEX_HTML.index('async function createPendingAnnotation(')
    create_end = INDEX_HTML.index('\nfunction findFieldElement(', create_start)
    create_body = INDEX_HTML[create_start:create_end]

    assert "loadAnnotations(tweetId)" not in create_body


def test_pending_highlight_preserves_existing_highlights():
    assert "async function loadAnnotationHighlights(tweetId)" in INDEX_HTML
    assert "applyHighlights(tweetId, anns);" in INDEX_HTML
    assert "function applyHighlights(tweetId, anns)" in INDEX_HTML
    assert "clearExisting" not in INDEX_HTML
