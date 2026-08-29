# 划线评论换行修复 + 粘贴图片上传 OSS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复划线评论换行不显示问题，并支持从剪贴板粘贴图片上传到阿里云 OSS 后在划线评论中展示。

**Architecture:** 前端（`weibospider/static/index.html`）用 `white-space: pre-wrap` 修复换行显示；新增 `POST /api/upload` 端点（oss2 SDK 上传 OSS），设置页新增「阿里云 OSS」配置区块（存 config 表，secret 打码），评论输入框 textarea 绑定 paste 事件上传图片并插入 `![图片](url)` markdown，渲染时用 `parseMarkdownImages` 把图片抽出到评论下方固定区域展示，编辑时重新从服务端取原始 comment（保留 markdown 原文）。

**Tech Stack:** Python 3.9 / Flask 2.3 / oss2 SDK / 原生 JS SPA / pytest

---

## 关键背景（实现前必读）

- **根因**：`renderAnnotationPanel`（`index.html:1626`）用 `esc(ann.comment)` 渲染，`\n` 被 HTML 折叠成空格。DB 中 `comment` 本身带 `\n`，是展示端丢失。
- **不要用 `<br>` 方案**：若把 `\n` 转 `<br>`，`editAnnotation` 用 `commentDiv.textContent` 回填 textarea 时 `<br>` 无 textContent，编辑时换行丢失。`pre-wrap` 让 `\n` 原样换行且 textContent 保留原文。
- **编辑必须保留 markdown**：`renderAnnotationPanel` 会把 `![图片](url)` 从显示文本中抽出到图片区，所以 `editAnnotation` 不能再依赖 `commentDiv.textContent`（会丢 markdown），改为从 `GET /api/annotations/<ann_id>` 重新取原始 comment。
- **oss2 懒加载**：`POST /api/upload` 函数内 `import oss2`（测试环境本地 venv 未装 oss2，模块级 import 会导致 `test_app.py` 无法导入 app；错误分支测试和 monkeypatch 模拟可绕开）。
- **测试运行**：`source venv/bin/activate && python -m pytest tests/ -q`（werkzeug 问题导致的 AttributeError 是环境问题，与本次改动无关）。

---

### Task 1: 前端 CSS 修复换行显示

**Files:**
- Modify: `weibospider/static/index.html:191-195`（`.annotation-selected-text` 和 `.annotation-comment` 两个 CSS 规则）
- Test: `tests/test_frontend.py`

- [ ] **Step 1: Write the failing test**

在 `tests/test_frontend.py` 末尾追加：

```python
def test_annotation_comment_preserves_newlines():
    assert 'white-space: pre-wrap' in INDEX_HTML
    assert 'annotation-comment {' in INDEX_HTML
    assert 'annotation-selected-text {' in INDEX_HTML
```

- [ ] **Step 2: Run test to verify it fails**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py::test_annotation_comment_preserves_newlines -q`
Expected: FAIL（`white-space: pre-wrap` 不在 index.html）

- [ ] **Step 3: Implement CSS fix**

编辑 `weibospider/static/index.html`，把 `.annotation-selected-text`（第 191-194 行）改为：

```css
.annotation-selected-text {
  background: #fff3cd; padding: 4px 6px; border-radius: 4px;
  font-size: 12px; color: #666; margin-bottom: 6px; cursor: pointer;
  white-space: pre-wrap; word-break: break-word;
}
```

把 `.annotation-comment`（第 195 行）改为：

```css
.annotation-comment { font-size: 13px; color: #333; line-height: 1.5; word-break: break-word; white-space: pre-wrap; }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py::test_annotation_comment_preserves_newlines -q`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add weibospider/static/index.html tests/test_frontend.py
git commit -m "fix: preserve newlines in annotation comment display (pre-wrap)"
```

---

### Task 2: 后端 OSS 配置存取（api_config + secret 打码）

**Files:**
- Modify: `weibospider/app.py:1268-1295`（`api_get_config`）
- Modify: `weibospider/app.py:1298-1344`（`api_set_config`）
- Test: `tests/test_app.py`（新增 `TestOssConfig` 类）

- [ ] **Step 1: Write the failing tests**

在 `tests/test_app.py` 顶部 import 区加入 `import io`（用于后续 Task 3 上传测试，先加上）。在 `TestAPI` 类后追加：

```python
class TestOssConfig:
    def test_config_get_has_oss_fields(self, client):
        import app as app_module
        app_module.DB.set_config('oss_access_key_id', 'AKID123')
        app_module.DB.set_config('oss_access_key_secret', 'SECRETVALUE123456789')
        app_module.DB.set_config('oss_bucket', 'mybucket')
        app_module.DB.set_config('oss_endpoint', 'oss-cn-hangzhou.aliyuncs.com')
        rv = client.get('/api/config')
        data = json.loads(rv.data)
        assert data['oss_access_key_id'] == 'AKID123'
        assert data['oss_bucket'] == 'mybucket'
        assert data['oss_endpoint'] == 'oss-cn-hangzhou.aliyuncs.com'
        assert 'SECRETVALUE' not in data['oss_access_key_secret_masked']
        assert data['oss_access_key_secret_masked']

    def test_config_post_oss_fields_then_get(self, client):
        rv = client.post('/api/config', json={
            'oss_access_key_id': 'AKID456',
            'oss_bucket': 'bkt2',
            'oss_endpoint': 'oss-cn-beijing.aliyuncs.com',
            'oss_url_prefix': 'https://img.example.com/',
        })
        assert rv.status_code == 200
        data = json.loads(client.get('/api/config').data)
        assert data['oss_access_key_id'] == 'AKID456'
        assert data['oss_bucket'] == 'bkt2'
        assert data['oss_endpoint'] == 'oss-cn-beijing.aliyuncs.com'
        assert data['oss_url_prefix'] == 'https://img.example.com/'
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `source venv/bin/activate && python -m pytest tests/test_app.py::TestOssConfig -q`
Expected: FAIL（`oss_access_key_id` 等 key 不存在于 GET 响应）

- [ ] **Step 3: Implement config GET/POST**

在 `app.py` 中 `@app.route('/api/config', methods=['GET'])`（第 1268 行）上方新增两个辅助函数：

```python
def _mask_secret(s):
    """打码密钥：短密钥全掩码，长密钥保留首尾 4 位。"""
    s = s or ''
    if not s:
        return ''
    if len(s) <= 8:
        return '*' * len(s)
    return s[:4] + '*' * (len(s) - 8) + s[-4:]


def _oss_config():
    return {
        'access_key_id': DB.get_config('oss_access_key_id', ''),
        'access_key_secret': DB.get_config('oss_access_key_secret', ''),
        'bucket': DB.get_config('oss_bucket', ''),
        'endpoint': DB.get_config('oss_endpoint', ''),
        'url_prefix': DB.get_config('oss_url_prefix', ''),
    }
```

在 `api_get_config` 返回的 dict（第 1283-1295 行）中、`**config,` 之前追加：

```python
        'oss_access_key_id': DB.get_config('oss_access_key_id', ''),
        'oss_access_key_secret_masked': _mask_secret(DB.get_config('oss_access_key_secret', '')),
        'oss_bucket': DB.get_config('oss_bucket', ''),
        'oss_endpoint': DB.get_config('oss_endpoint', ''),
        'oss_url_prefix': DB.get_config('oss_url_prefix', ''),
```

在 `api_set_config` 中、`schedule_keys` 解析代码之前追加：

```python
    oss_fields = {
        'oss_access_key_id': None,
        'oss_access_key_secret': None,
        'oss_bucket': None,
        'oss_endpoint': None,
        'oss_url_prefix': None,
    }
    for key in oss_fields:
        if key in data and data[key] is not None:
            DB.set_config(key, str(data[key]))
            updated[key] = data[key]
    if 'oss_access_key_id' in updated or 'oss_bucket' in updated or 'oss_endpoint' in updated:
        DB.insert_log('config', 'set_oss', status='success', user='web')
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `source venv/bin/activate && python -m pytest tests/test_app.py::TestOssConfig -q`
Expected: PASS

- [ ] **Step 5: Run full app test suite (regression)**

Run: `source venv/bin/activate && python -m pytest tests/test_app.py -q`
Expected: 全部通过（新增 2 个 + 原有）

- [ ] **Step 6: Commit**

```bash
git add weibospider/app.py tests/test_app.py
git commit -m "feat: add OSS config storage with masked secret in api/config"
```

---

### Task 3: 后端 POST /api/upload 上传 OSS

**Files:**
- Modify: `weibospider/app.py`（新增 `POST /api/upload` 端点）
- Modify: `weibospider/requirements.txt`（新增 `oss2`）
- Test: `tests/test_app.py`（新增 `TestUpload` 类）

- [ ] **Step 1: Write the failing tests**

在 `TestOssConfig` 类后追加：

```python
class TestUpload:
    def _upload(self, client, filename='a.png', ctype='image/png', data=b'x'):
        return client.post('/api/upload',
                           data={'file': (io.BytesIO(data), filename, ctype)},
                           content_type='multipart/form-data')

    def test_upload_non_image_rejected(self, client):
        rv = self._upload(client, filename='a.txt', ctype='text/plain')
        assert rv.status_code == 400
        assert '图片' in json.loads(rv.data)['error']

    def test_upload_oversize_rejected(self, client):
        rv = self._upload(client, data=b'x' * (8 * 1024 * 1024 + 1))
        assert rv.status_code == 400
        assert '8MB' in json.loads(rv.data)['error']

    def test_upload_missing_oss_config_rejected(self, client):
        rv = self._upload(client)
        assert rv.status_code == 400
        assert 'OSS' in json.loads(rv.data)['error']

    def test_upload_success(self, client, monkeypatch):
        import app as app_module
        import types
        app_module.DB.set_config('oss_access_key_id', 'AK')
        app_module.DB.set_config('oss_access_key_secret', 'SK')
        app_module.DB.set_config('oss_bucket', 'bkt')
        app_module.DB.set_config('oss_endpoint', 'oss-cn-hangzhou.aliyuncs.com')
        captured = {}

        class FakeAuth:
            def __init__(self, key, secret):
                pass

        class FakeBucket:
            def __init__(self, auth, endpoint, bucket):
                captured['endpoint'] = endpoint
                captured['bucket'] = bucket

            def put_object(self, key, data, headers=None):
                captured['key'] = key
                captured['data'] = data

        fake = types.ModuleType('oss2')
        fake.Auth = FakeAuth
        fake.Bucket = FakeBucket
        monkeypatch.setitem(sys.modules, 'oss2', fake)

        rv = self._upload(client, data=b'pngdata')
        assert rv.status_code == 200
        data = json.loads(rv.data)
        assert data['url'].startswith('https://bkt.oss-cn-hangzhou.aliyuncs.com/annotations/')
        assert captured['key'].startswith('annotations/')
        assert captured['key'].endswith('.png')
        assert captured['data'] == b'pngdata'
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `source venv/bin/activate && python -m pytest tests/test_app.py::TestUpload -q`
Expected: FAIL（404，`/api/upload` 路由不存在）

- [ ] **Step 3: Implement the upload endpoint**

在 `app.py` 的 `@app.route('/api/annotations/<ann_id>', methods=['DELETE'])` 函数之后（约第 999 行后）新增：

```python
_IMG_EXT = {'jpeg': 'jpg', 'jpg': 'jpg', 'png': 'png', 'gif': 'gif', 'webp': 'webp'}


def _image_ext(ctype):
    return _IMG_EXT.get(ctype.split('/')[-1], 'png')


@app.route('/api/upload', methods=['POST'])
def api_upload():
    """上传剪贴板图片到阿里云 OSS，返回可访问 URL。"""
    f = request.files.get('file')
    if f is None:
        return jsonify({'error': '缺少文件'}), 400
    ctype = (f.content_type or '').lower()
    if not ctype.startswith('image/'):
        return jsonify({'error': '仅支持图片文件'}), 400
    data = f.read()
    if len(data) > 8 * 1024 * 1024:
        return jsonify({'error': '图片大小超过 8MB'}), 400
    cfg = _oss_config()
    missing = [k for k in ('access_key_id', 'access_key_secret', 'bucket', 'endpoint')
               if not cfg.get(k)]
    if missing:
        return jsonify({'error': '请先在设置中配置 OSS'}), 400
    key = 'annotations/%s.%s' % (uuid.uuid4().hex, _image_ext(ctype))
    try:
        import oss2
        auth = oss2.Auth(cfg['access_key_id'], cfg['access_key_secret'])
        bucket = oss2.Bucket(auth, 'https://' + cfg['endpoint'], cfg['bucket'])
        bucket.put_object(key, data, headers={'Content-Type': ctype})
    except Exception as e:
        logger.exception('OSS upload failed')
        DB.insert_log('annotation', 'upload_error', detail=str(e)[:100],
                      status='error', user='web')
        return jsonify({'error': 'OSS 上传失败: %s' % e}), 500
    prefix = cfg.get('url_prefix') or 'https://%s.%s/' % (cfg['bucket'], cfg['endpoint'])
    url = prefix.rstrip('/') + '/' + key
    DB.insert_log('annotation', 'upload', detail=key, status='success', user='web')
    return jsonify({'url': url})
```

在 `weibospider/requirements.txt` 追加一行：

```
oss2
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `source venv/bin/activate && python -m pytest tests/test_app.py::TestUpload -q`
Expected: 4 个全部 PASS

- [ ] **Step 5: Run full app test suite (regression)**

Run: `source venv/bin/activate && python -m pytest tests/test_app.py -q`
Expected: 全部通过

- [ ] **Step 6: Commit**

```bash
git add weibospider/app.py weibospider/requirements.txt tests/test_app.py
git commit -m "feat: add POST /api/upload endpoint to upload images to Alibaba OSS"
```

---

### Task 4: 前端设置页「阿里云 OSS」区块

**Files:**
- Modify: `weibospider/static/index.html:312-323`（雪球 Cookie 区块后新增 OSS 区块）
- Modify: `weibospider/static/index.html:476-528`（`loadConfig` 回填 OSS 字段）
- Modify: `weibospider/static/index.html:564-594`（新增 `saveOssConfig`，仿 `saveXqCookie`）
- Test: `tests/test_frontend.py`

- [ ] **Step 1: Write the failing tests**

在 `tests/test_frontend.py` 末尾追加：

```python
def test_oss_config_section_exists():
    assert '阿里云 OSS' in INDEX_HTML
    assert 'id="oss-access-key-id"' in INDEX_HTML
    assert 'id="oss-access-key-secret"' in INDEX_HTML
    assert 'id="oss-bucket"' in INDEX_HTML
    assert 'id="oss-endpoint"' in INDEX_HTML
    assert 'id="oss-url-prefix"' in INDEX_HTML
    assert 'async function saveOssConfig()' in INDEX_HTML
```

- [ ] **Step 2: Run test to verify it fails**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py::test_oss_config_section_exists -q`
Expected: FAIL

- [ ] **Step 3: Add OSS settings HTML section**

在 `index.html` 第 323 行（`</div>` 结束雪球 Cookie section）之后、第 325 行（`<div class="config-section">` 时间范围）之前插入：

```html
      <div class="config-section">
        <h3>阿里云 OSS</h3>
        <div id="oss-status" style="font-size:11px;color:#888;margin-bottom:6px;"></div>
        <div class="uid-input-row">
          <input type="text" id="oss-access-key-id" placeholder="AccessKey ID" style="flex:1;">
        </div>
        <div class="uid-input-row" style="margin-top:6px;">
          <input type="password" id="oss-access-key-secret" placeholder="AccessKey Secret（留空则不改动）" style="flex:1;">
        </div>
        <div class="uid-input-row" style="margin-top:6px;">
          <input type="text" id="oss-bucket" placeholder="Bucket 名称" style="flex:1;">
        </div>
        <div class="uid-input-row" style="margin-top:6px;">
          <input type="text" id="oss-endpoint" placeholder="Endpoint 如 oss-cn-hangzhou.aliyuncs.com" style="flex:1;">
        </div>
        <div class="uid-input-row" style="margin-top:6px;">
          <input type="text" id="oss-url-prefix" placeholder="URL 前缀（可空，默认 bucket.endpoint）" style="flex:1;">
          <button class="btn btn-sm btn-primary" onclick="saveOssConfig()">保存</button>
        </div>
      </div>
```

- [ ] **Step 4: Add loadConfig 回填**

在 `loadConfig`（第 476-528 行）中、`if (data.xq_cookie)` 分支之后追加：

```js
    $('oss-access-key-id').value = data.oss_access_key_id || '';
    $('oss-bucket').value = data.oss_bucket || '';
    $('oss-endpoint').value = data.oss_endpoint || '';
    $('oss-url-prefix').value = data.oss_url_prefix || '';
    $('oss-access-key-secret').value = '';
    if (data.oss_access_key_secret_masked) {
      $('oss-status').textContent = '已配置: ' + data.oss_access_key_secret_masked;
      $('oss-status').style.color = '#27ae60';
    } else {
      $('oss-status').textContent = '未配置（粘贴图片上传将失败）';
      $('oss-status').style.color = '#e74c3c';
    }
```

- [ ] **Step 5: Add saveOssConfig 函数**

在 `saveXqCookie`（第 578-594 行）之后追加：

```js
async function saveOssConfig() {
  const payload = {
    oss_access_key_id: $('oss-access-key-id').value.trim(),
    oss_bucket: $('oss-bucket').value.trim(),
    oss_endpoint: $('oss-endpoint').value.trim(),
    oss_url_prefix: $('oss-url-prefix').value.trim(),
  };
  const secret = $('oss-access-key-secret').value.trim();
  if (secret) payload.oss_access_key_secret = secret;
  if (!payload.oss_access_key_id || !payload.oss_bucket || !payload.oss_endpoint) {
    toast('请填写 AK ID / Bucket / Endpoint', true); return;
  }
  try {
    await fetch('/api/config', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(payload)
    });
    toast('OSS 配置已保存');
    loadConfig();
  } catch(e) { toast('保存失败', true); }
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py::test_oss_config_section_exists -q`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add weibospider/static/index.html tests/test_frontend.py
git commit -m "feat: add OSS config section to settings panel"
```

---

### Task 5: 前端评论输入框 paste 图片上传

**Files:**
- Modify: `weibospider/static/index.html:1535-1589`（`showAnnotationInput` 中 textarea 绑定 paste）
- Test: `tests/test_frontend.py`

- [ ] **Step 1: Write the failing tests**

在 `tests/test_frontend.py` 末尾追加：

```python
def test_paste_image_handler_exists():
    assert "addEventListener('paste'" in INDEX_HTML
    assert 'clipboardData' in INDEX_HTML
    assert 'getAsFile()' in INDEX_HTML
    assert 'uploadAnnotationImage' in INDEX_HTML
    assert "fetch('/api/upload'" in INDEX_HTML
    assert 'insertAtCursor' in INDEX_HTML
    assert '![图片](' in INDEX_HTML
```

- [ ] **Step 2: Run test to verify it fails**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py::test_paste_image_handler_exists -q`
Expected: FAIL

- [ ] **Step 3: Implement paste handler in showAnnotationInput**

在 `showAnnotationInput` 中、`textarea.addEventListener('keydown', ...)`（第 1583-1588 行）之后追加：

```js
  textarea.addEventListener('paste', async (e) => {
    const items = e.clipboardData && e.clipboardData.items;
    if (!items) return;
    for (const item of items) {
      if (item.kind === 'file' && item.type.indexOf('image/') === 0) {
        e.preventDefault();
        const file = item.getAsFile();
        await uploadAnnotationImage(file, textarea);
        break;
      }
    }
  });
```

- [ ] **Step 4: Add uploadAnnotationImage and insertAtCursor 函数**

在 `showAnnotationInput` 函数结束后（`}` 之后、`async function loadAnnotations` 之前，约第 1589 行）追加：

```js
async function uploadAnnotationImage(file, textarea) {
  const fd = new FormData();
  fd.append('file', file, file.name || 'paste.png');
  try {
    const resp = await fetch('/api/upload', { method: 'POST', body: fd });
    const data = await resp.json();
    if (resp.ok && data.url) {
      insertAtCursor(textarea, '![图片](' + data.url + ')');
      toast('图片已上传');
    } else {
      toast(data.error || '图片上传失败', true);
    }
  } catch(e) { toast('图片上传失败', true); }
}

function insertAtCursor(textarea, text) {
  const start = textarea.selectionStart;
  const end = textarea.selectionEnd;
  textarea.value = textarea.value.slice(0, start) + text + textarea.value.slice(end);
  textarea.focus();
  const pos = start + text.length;
  textarea.setSelectionRange(pos, pos);
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py::test_paste_image_handler_exists -q`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add weibospider/static/index.html tests/test_frontend.py
git commit -m "feat: paste image from clipboard uploads to OSS and inserts markdown"
```

---

### Task 6: 前端评论渲染 markdown 图片 + 编辑保留原文

**Files:**
- Modify: `weibospider/static/index.html:1609-1641`（`renderAnnotationPanel` 拆图渲染）
- Modify: `weibospider/static/index.html:1795-1811`（`editAnnotation` 从服务端取原始 comment）
- Modify: `weibospider/static/index.html:195`（`.annotation-comment` CSS 追加图片区样式）
- Test: `tests/test_frontend.py`

- [ ] **Step 1: Write the failing tests**

在 `tests/test_frontend.py` 末尾追加：

```python
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py::test_markdown_image_parser_exists tests/test_frontend.py::test_edit_annotation_fetches_raw_comment -q`
Expected: FAIL

- [ ] **Step 3: Add CSS for image area**

在 `.annotation-comment` 规则（第 195 行）之后追加：

```css
.annotation-comment-images { margin-top: 6px; display: flex; flex-wrap: wrap; gap: 6px; }
.annotation-comment-img {
  max-width: 90px; max-height: 90px; border-radius: 4px;
  object-fit: cover; cursor: zoom-in; background: #f0f0f0;
}
```

- [ ] **Step 4: Add parseMarkdownImages 函数**

在 `renderAnnotationPanel`（第 1609 行）之前追加：

```js
function parseMarkdownImages(comment) {
  const images = [];
  const text = String(comment || '').replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (m, alt, url) => {
    images.push(url);
    return '';
  });
  return { text: text, images: images };
}
```

- [ ] **Step 5: Rewrite renderAnnotationPanel 拆图渲染**

把 `renderAnnotationPanel` 中 `anns.forEach(ann => { ... });` 整个循环体（第 1621-1637 行）替换为：

```js
  anns.forEach(ann => {
    const body = document.createElement('div');
    body.className = 'annotation-card-body';
    body.id = 'ann-body-' + ann.id;
    const parsed = parseMarkdownImages(ann.comment);
    const commentHtml = ann.comment
      ? `<div class="annotation-comment">${esc(parsed.text)}</div>`
      : `<div class="annotation-comment" style="color:#aaa;font-style:italic;">未输入评论</div>`;
    const imgsHtml = parsed.images.length
      ? `<div class="annotation-comment-images">${parsed.images.map(u => `<img src="${esc(u)}" class="annotation-comment-img" loading="lazy" onclick="openLightbox(this.src)">`).join('')}</div>`
      : '';
    body.innerHTML = `
      <div class="annotation-selected-text" onclick="scrollToHighlight('${tweetId}', '${ann.id}')">"${esc(ann.selected_text)}"</div>
      ${commentHtml}
      ${imgsHtml}
      <div class="annotation-actions">
        <a onclick="editAnnotation('${ann.id}', '${tweetId}')">编辑</a>
        <a class="del" onclick="deleteAnnotation('${ann.id}', '${tweetId}')">删除</a>
      </div>
    `;
    card.appendChild(body);
  });
```

- [ ] **Step 6: Rewrite editAnnotation 从服务端取原文**

把 `editAnnotation` 函数（第 1795-1811 行）整体替换为：

```js
async function editAnnotation(annId, tweetId) {
  const body = $('ann-body-' + annId);
  if (!body) return;
  let rawComment = '';
  try {
    const resp = await fetch(`/api/annotations/${annId}`);
    if (resp.ok) rawComment = (await resp.json()).comment || '';
  } catch(e) { /* keep empty */ }
  const commentDiv = body.querySelector('.annotation-comment');
  const textarea = document.createElement('textarea');
  textarea.value = rawComment;
  textarea.style.cssText = 'width:100%;border:1px solid #4a9eff;border-radius:6px;padding:8px;font-size:13px;resize:none;height:60px;outline:none;';
  commentDiv.replaceWith(textarea);
  textarea.focus();

  const actionsDiv = body.querySelector('.annotation-actions');
  actionsDiv.innerHTML = `
    <a onclick="saveAnnotationEdit('${annId}', '${tweetId}')">保存</a>
    <a class="del" onclick="loadAnnotations('${tweetId}')">取消</a>
  `;
}
```

注意：`saveAnnotationEdit`（第 1813 行）中 `body.querySelector('textarea')` 取 value 后 `textarea.value.trim()` 会保留 markdown 原文，PUT 到服务端即可，无需改动。但 `if (!comment) { toast('请输入评论', true); return; }` 会拦截「只有图片没有文字」的评论 —— 需要把该判断改为 `if (!comment && !body.querySelector('.annotation-comment-images'))` 以便纯图片评论可保存。编辑 `saveAnnotationEdit` 中对应行：

```js
  const comment = textarea.value.trim();
  if (!comment && !body.querySelector('.annotation-comment-images')) { toast('请输入评论', true); return; }
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py::test_markdown_image_parser_exists tests/test_frontend.py::test_edit_annotation_fetches_raw_comment -q`
Expected: PASS

- [ ] **Step 8: Run full frontend test suite (regression)**

Run: `source venv/bin/activate && python -m pytest tests/test_frontend.py -q`
Expected: 全部通过

- [ ] **Step 9: Run the whole test suite**

Run: `source venv/bin/activate && python -m pytest tests/ -q`
Expected: 除已知 werkzeug 环境问题（test_app/test_integration 中依赖 Flask test client fixture 的 AttributeError）外全部通过

- [ ] **Step 10: Commit**

```bash
git add weibospider/static/index.html tests/test_frontend.py
git commit -m "feat: render pasted OSS images in annotation comments, preserve markdown on edit"
```

---

## 自检记录（Self-Review）

**Spec 覆盖：**
- 换行修复（spec「改动一」）→ Task 1 ✓
- OSS 配置存 config 表 + secret 打码（spec「后端」）→ Task 2 ✓
- `POST /api/upload` 校验 + oss2 上传 + URL 前缀（spec「上传端点」）→ Task 3 ✓
- 设置页 UI（spec「设置页区块」）→ Task 4 ✓
- textarea paste 上传 + markdown 插入（spec「输入框 paste 上传」）→ Task 5 ✓
- markdown 解析渲染到评论下方固定区域 + 编辑保留原文（spec「markdown 图片解析渲染」「编辑时」）→ Task 6 ✓
- 依赖 oss2（spec「依赖」）→ Task 3 ✓
- 测试三件套（test_frontend / test_app / test_db）→ Task 2/3 含 test_db？见下方补充

**修正：** spec 测试清单含 `tests/test_db.py`「OSS 配置存取」，但 `DB.set_config/get_config` 已有既有测试覆盖（`test_keepalive.py` 用到），且本方案 OSS 配置完全走既有 `set_config`/`get_config`，不新增 DB 代码，故无需新增 test_db.py 用例（不满足新增代码则新增测试的 TDD 原则）。

**类型一致性：**
- `_mask_secret` / `_oss_config` / `_image_ext` 在 Task 2/3 定义并在后续 Task 使用，签名一致。
- `parseMarkdownImages` 返回 `{text, images}`，Task 6 渲染处按此解构。
- `uploadAnnotationImage(file, textarea)` / `insertAtCursor(textarea, text)` 签名与 Task 5 调用一致。

**无占位符：** 每个 step 均含完整代码与精确命令。
