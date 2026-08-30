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


def test_xueqiu_tab_exists():
    assert 'data-tab="xueqiu"' in INDEX_HTML
    assert '雪球同步' in INDEX_HTML
    assert 'platform-badge' in INDEX_HTML


def test_xueqiu_functions_exist():
    assert "async function triggerXueqiu(mode)" in INDEX_HTML
    assert "async function saveXqCookie()" in INDEX_HTML
    assert "fetch('/api/crawl/xueqiu'" in INDEX_HTML
    assert "fetch(`/api/tweets?page=${currentPage}&per_page=${perPage}&deleted=${deleted}&platform=${platform}${uidParam}`)" in INDEX_HTML
    assert "xqimg.imedao.com" in INDEX_HTML


def test_xueqiu_comments_button_exists():
    assert 'id="btn-xueqiu-comments"' in INDEX_HTML
    assert '雪球评论' in INDEX_HTML
    assert "async function triggerXueqiuComments(mode)" in INDEX_HTML
    assert "fetch('/api/crawl/xueqiu-comments'" in INDEX_HTML
    assert "data.xueqiu_comment" in INDEX_HTML


def test_comment_images_rendered():
    assert 'c.pic_urls' in INDEX_HTML
    assert 's.pic_urls' in INDEX_HTML
    assert 'class="comment-pic"' in INDEX_HTML
    assert 'comment-images' in INDEX_HTML
    assert 'openLightboxFromComment' in INDEX_HTML


def test_xqimg_fullsize_uses_raw():
    assert r"!thumb\.jpg" in INDEX_HTML
    assert "!raw.jpg" in INDEX_HTML
    assert "xqimg.imedao.com" in INDEX_HTML


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


def test_render_card_uses_comments_count_field():
    assert "t.comments_count" in INDEX_HTML


def test_render_card_does_not_embed_comments_inline():
    card_start = INDEX_HTML.index("function renderCard(t, container)")
    card_end = INDEX_HTML.index("function toggleComments", card_start)
    card_body = INDEX_HTML[card_start:card_end]
    assert "comments.map(c => renderComment(c)).join('')" not in card_body


def test_toggle_comments_lazy_loads():
    toggle_start = INDEX_HTML.index("function toggleComments(")
    toggle_end = INDEX_HTML.index("async function crawlComments", toggle_start)
    toggle_body = INDEX_HTML[toggle_start:toggle_end]
    assert "fetch(`/api/tweets/${id}`)" in toggle_body
    assert "renderComment" in toggle_body


def test_cookie_expired_modal_exists():
    assert 'id="cookie-modal"' in INDEX_HTML
    assert 'cookie-modal-input' in INDEX_HTML
    assert '保存并重试' in INDEX_HTML
    assert '稍后再说' in INDEX_HTML


def test_poll_checks_cookie_expired():
    poll_start = INDEX_HTML.index("async function pollCrawlStatus()")
    next_fn = INDEX_HTML.find("function ", poll_start + 10)
    poll_body = INDEX_HTML[poll_start:next_fn if next_fn != -1 else len(INDEX_HTML)]
    assert "cookie_expired" in poll_body
    assert "Cookie 已过期" in poll_body


def test_cookie_modal_save_function_exists():
    assert "function showCookieModal(" in INDEX_HTML
    assert "function dismissCookieModal(" in INDEX_HTML
    assert "async function saveCookieFromModal(" in INDEX_HTML
    assert "fetch('/api/config'" in INDEX_HTML


def test_annotation_comment_preserves_newlines():
    comment_css = INDEX_HTML[INDEX_HTML.index('.annotation-comment {'):]
    assert 'white-space: pre-wrap' in comment_css
    selected_css = INDEX_HTML[INDEX_HTML.index('.annotation-selected-text {'):]
    assert 'white-space: pre-wrap' in selected_css


def test_oss_config_section_exists():
    assert '阿里云 OSS' in INDEX_HTML
    assert 'id="oss-access-key-id"' in INDEX_HTML
    assert 'id="oss-access-key-secret"' in INDEX_HTML
    assert 'id="oss-bucket"' in INDEX_HTML
    assert 'id="oss-endpoint"' in INDEX_HTML
    assert 'id="oss-url-prefix"' in INDEX_HTML
    assert 'async function saveOssConfig()' in INDEX_HTML


def test_paste_image_handler_exists():
    assert "addEventListener('paste'" in INDEX_HTML
    assert 'clipboardData' in INDEX_HTML
    assert 'getAsFile()' in INDEX_HTML
    assert 'uploadAnnotationImage' in INDEX_HTML
    assert "fetch('/api/upload'" in INDEX_HTML
    assert 'insertAtCursor' in INDEX_HTML
    assert '![图片](' in INDEX_HTML


def test_markdown_image_parser_exists():
    assert 'function parseMarkdownImages(' in INDEX_HTML
    assert 'annotation-comment-images' in INDEX_HTML
    assert 'annotation-comment-img' in INDEX_HTML
    assert 'openLightbox(this.src)' in INDEX_HTML


def test_edit_annotation_fetches_raw_comment():
    edit_start = INDEX_HTML.index('async function editAnnotation(')
    edit_end = INDEX_HTML.index('async function saveAnnotationEdit(', edit_start)
    edit_body = INDEX_HTML[edit_start:edit_end]
    assert '`/api/annotations/${annId}`' in edit_body


def test_notes_tab_exists():
    assert 'data-tab="notes"' in INDEX_HTML
    assert '>笔记</button>' in INDEX_HTML


def test_notes_tab_after_ps_tab():
    ps_idx = INDEX_HTML.index('data-tab="ps"')
    notes_idx = INDEX_HTML.index('data-tab="notes"')
    assert notes_idx > ps_idx


def test_load_notes_function_exists():
    assert 'async function loadNotes()' in INDEX_HTML
    assert "fetch('/api/notes')" in INDEX_HTML
    assert 'loadNotes();' in INDEX_HTML


def test_switch_tab_handles_notes():
    assert "isNotes = tab === 'notes'" in INDEX_HTML
    assert "isNotes" in INDEX_HTML


def test_notes_uses_ps_mode():
    notes_start = INDEX_HTML.index("isNotes = tab === 'notes'")
    notes_end = INDEX_HTML.index('function loadPs', notes_start)
    notes_body = INDEX_HTML[notes_start:notes_end]
    assert 'ps-mode' in notes_body


def test_edit_annotation_textarea_binds_paste_handler():
    edit_start = INDEX_HTML.index('async function editAnnotation(')
    edit_end = INDEX_HTML.index('async function saveAnnotationEdit(', edit_start)
    edit_body = INDEX_HTML[edit_start:edit_end]
    assert 'bindAnnotationImagePaste' in edit_body


def test_create_annotation_textarea_binds_shared_paste_handler():
    show_start = INDEX_HTML.index('function showAnnotationInput(')
    show_end = INDEX_HTML.index('function bindAnnotationImagePaste', show_start)
    show_body = INDEX_HTML[show_start:show_end]
    assert 'bindAnnotationImagePaste' in show_body
    assert "addEventListener('paste'" not in show_body


class TestSearchUI:
    def test_search_input_exists(self):
        assert 'id="search-input"' in INDEX_HTML

    def test_search_tab_exists(self):
        assert 'data-tab="search"' in INDEX_HTML
        assert 'id="search-view"' in INDEX_HTML

    def test_calls_search_api(self):
        assert '/api/search?' in INDEX_HTML

    def test_mark_style_defined(self):
        assert 'mark {' in INDEX_HTML or '.search-hl mark' in INDEX_HTML

    def test_enter_key_triggers_search(self):
        assert 'doSearch' in INDEX_HTML

    def test_render_card_accepts_container_arg(self):
        assert 'function renderCard(t, container) {' in INDEX_HTML
        assert "container = container || $('cards-container');" in INDEX_HTML

    def test_search_reuses_render_card(self):
        """Search results must reuse renderCard so styling/notes panel match."""
        start = INDEX_HTML.index('function renderSearchResults(')
        end = INDEX_HTML.index('function renderSearchPager(')
        body = INDEX_HTML[start:end]
        assert 'renderCard(r, box)' in body
        # the old simplified card markup must be gone
        assert '<div class="card"><div class="card-body">' not in body
        # still shows why the row matched
        assert 'search-hit' in body
        assert 'search-src' in body
        assert 'r.highlight' in body

    def test_search_dedupes_by_tweet_id(self):
        start = INDEX_HTML.index('function renderSearchResults(')
        end = INDEX_HTML.index('function renderSearchPager(')
        body = INDEX_HTML[start:end]
        assert 'seen.has(r.tweet_id)' in body

    def test_search_view_inside_app_container(self):
        """search-view must live inside #app so cards get the same width
        as the normal list, otherwise the floating notes panel overflows."""
        app_start = INDEX_HTML.index('<div id="app">')
        sv = INDEX_HTML.index('<div id="search-view"')
        cards = INDEX_HTML.index('<div id="cards-container">')
        assert app_start < cards < sv

    def test_entering_search_clears_main_list(self):
        """The hidden main list holds duplicate annotation-panel-<id> ids;
        it must be cleared so search cards' notes panels resolve correctly."""
        start = INDEX_HTML.index('function switchTab(')
        end = INDEX_HTML.index('async function loadPs()')
        body = INDEX_HTML[start:end]
        assert "} else if (isSearch) {" in body
        idx = body.index('} else if (isSearch) {')
        branch = body[idx:body.index('} else if (isPs)', idx)]
        assert "$('cards-container').innerHTML = '';" in branch
