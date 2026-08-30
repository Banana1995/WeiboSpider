# 搜索结果展示与定位设计

- 日期：2026-08-30
- 状态：待审阅
- 范围：`/api/search` 后端聚合 + `static/index.html` 搜索结果渲染与评论定位

## 1. 背景与问题

全局搜索已上线（见 `2026-08-29-global-search-design.md`），但搜索结果展示有三个缺陷：

1. **只展示一部分内容**：后端用 `snippet(search_index, 3, ..., 12)` 只给 12 个 token 的片段，命中内容被截断成 `回复@逆旅球徒:国家统计局官…`。完整正文/评论/笔记内容其实已返回给前端（`t.*`），但没展示。
2. **评论命中无法定位上下文**：命中评论时，评论区是折叠的，且评论 DOM 上没有可定位的锚点；命中若落在子评论，还可能被"展开 N 条回复"折叠隐藏。用户点击后无法看到该评论在对话中的位置。
3. **分页/计数不一致（上次遗留缺陷）**：后端 `total` 是**命中条目数**（含重复），前端又按微博去重，导致"共 75 条"实际只显示 18 条、每页数量不稳定、同一条微博跨页可能重复出现。

## 2. 目标

1. **微博命中**：卡片默认展示完整正文（含转发原文），命中关键词用 `<mark>` 高亮，不再显示截断片段。
2. **评论/笔记命中**：卡片上显示命中的完整内容（高亮），不再截断。
3. **评论定位**：点击命中片段 → 自动展开评论区 → 若命中为被折叠的子评论则先展开所在折叠组 → 滚动到该评论并临时高亮。
4. **修好分页/去重**：按微博聚合，`total` 改为去重后的微博数，一处微博的多处命中全部列出，"共 N 条" = 真实卡片数。

## 3. 关键约束（均已实测）

- **`snippet()` 不能在聚合上下文使用**：`GROUP BY` + `json_group_array` 里写 `snippet(...)` 报 `unable to use function snippet in the requested context`（含 CTE、`AS MATERIALIZED` 均无效）。→ 高亮改到 Python 端做。
- **`search_index.text` 列丢失字段边界**：`_index_tweet` 存的是 `content + ' ' + retweet_content`，`_index_annotation_row` 存的是 `comment + ' ' + selected_text`（`db.py:293-313`）。无法区分正文/转发、笔记/划线原文。→ 完整文本必须按 `doc_id` 回 `comments`/`annotations` 源表批量补齐，而非用 `text` 列。
- **`GROUP BY t.id` + 聚合** 在 SQLite 3.43.2（本地）/ 3.46.1（生产）均可用；`json_group_array(json_object(...))` 可用；`COUNT(*) FROM (subquery GROUP BY)` 可用于去重计数。
- **`/api/tweets/<id>` 一次返回该微博全部评论**（无分页），命中评论必然在 DOM 中，定位无需后端改分页。
- **评论 DOM 无锚点**：`renderComment`（`index.html:919-941`）顶层 `<div class="comment">`、`renderSubCommentInner` 子评论 `<div class="comment-reply">`，均无 id/data 属性；子评论 >5 条折叠（`index.html:926`）。
- **`make_highlight` 是截断的**（`search.py:108-134`，前后各 `context=20` 字加省略号），不能满足"完整内容"需求。
- 命中评论里约 1/3 是子评论（实测：22 条命中评论中 7 条子评论），线上有 248 个折叠组，个别微博 700+ 评论。

## 4. 设计

### 4.1 API 数据结构

`GET /api/search` 返回改为：

```json
{
  "total": 123,
  "page": 1,
  "per_page": 20,
  "query": "统计局",
  "elapsed_ms": 2.1,
  "results": [
    {
      "id": "5336979547884218",
      "content": "完整正文……",
      "screen_name": "…", "created_at": "…", "platform": "weibo",
      "pic_urls": [], "reposts_count": 0, "…（完整微博字段 t.*）",
      "annotations_list": [ … ],
      "hits": [
        { "source_type": "tweet", "highlight": "完整正文，<mark>统计局</mark>高亮" },
        { "source_type": "comment", "doc_id": "5336991417764213", "highlight": "完整评论，<mark>统计局</mark>高亮" },
        { "source_type": "annotation", "doc_id": "06b88d4e-…", "highlight": "笔记完整内容，<mark>统计局</mark>高亮" }
      ]
    }
  ]
}
```

要点：
- `total` = 去重后的微博数；`results` 每项一条微博，`hits` 聚合该微博的全部命中。
- 每个 `hit` 的 `highlight` 是**完整文本 + `<mark>` 高亮**（不再截断），由 Python 端 `highlight_all()` 生成。
- `source_type='tweet'` 的 hit 不带 `doc_id`（无需定位，正文就在卡片上）；comment/annotation 的 hit 带 `doc_id` 供定位。
- 排序：`t.created_at DESC`（微博按最新）；同一条微博内 hit 固定顺序 `tweet → comment → annotation`，同类型按 `doc_id` 稳定序。
- `source_type` 过滤、`start_date`/`end_date` 过滤行为不变。

### 4.2 后端聚合 SQL（`search.py` 的 `build_search_sql`）

两条路径都改为「子查询命中 → GROUP BY tweet_id → 聚合 doc 列表」，不产生 `snippet()`。

**Path A（MATCH，len ≥ 3）**：

```sql
SELECT t.id AS tweet_id,
       COUNT(*) AS hit_count,
       json_group_array(json_object(
           'doc_id', s.doc_id,
           'source_type', s.source_type
       )) AS hits,
       t.*
  FROM search_index s
  JOIN tweets t ON t.id = s.tweet_id
 WHERE search_index MATCH ?
   AND t.deleted = 0
   {extra}
 GROUP BY t.id
 ORDER BY t.created_at DESC
 LIMIT ? OFFSET ?
```

**Path B（LIKE，len < 3）**：三个源表的 `UNION ALL` 子查询保持原样（`s` 别名），外层改为同样的 `GROUP BY t.id` + `json_group_array` 聚合（`matched_text` 列不再需要，见 4.3）。

**去重计数**（`db.py` 的 `search()`）：把 `LIMIT ? OFFSET ?` 去掉后，再包一层计数：

```sql
SELECT COUNT(*) FROM (<聚合子查询，去掉 LIMIT/OFFSET>)
```

`total` 即去重微博数。

### 4.3 Python 端命中文本补齐 + 全文高亮（`db.py` 的 `search()` + `search.py`）

`search()` 拿到聚合行后：

1. 反序列化 `pic_urls`/`retweet_pic_urls`（保持现状，`db.py:960-965`）。
2. 解析 `hits` JSON 数组（`json.loads`）。
3. **批量补齐完整文本**（按 `source_type` 分组，一次 `IN` 查询取回）：
   - `tweet` → 文本 = `t.content`（正文；转发原文由 `renderCard` 的 `renderRetweet` 展示，不重复）。若 content 为空但 retweet_content 非空，退化为 retweet_content。
   - `comment` → 文本 = `comments.content`（按 `doc_id` 查 `comments`）。
   - `annotation` → 文本 = `annotations.comment`（按 `doc_id` 查 `annotations`；selected_text 作为划线原文在右侧面板已有，不重复进 hit）。
4. 对每个 hit 调 `highlight_all(text, q)` 生成完整高亮文本；补齐失败的 hit 丢弃（其 doc 已被删除的竞态），若补齐后某微博 `hits` 为空则丢弃该结果。
5. `_attach_annotations(out['results'])`（保持现状，供右侧笔记面板）。

**`search.py` 新增 `highlight_all(text, q)`**：

- 先 `html.escape` 全文本，再大小写不敏感地把**所有**出现的 `q` 包 `<mark>`（非首处截断）。
- `q` 为空时返回转义后的全文本。
- 文本为空返回 `''`。
- 复用/改造现有 `escape_snippet` 的安全思路：先转义再插入 `<mark>`，杜绝 XSS。

`make_highlight`（截断版）改造后不再被 `search()` 调用；若无其他调用点则删除。

### 4.4 前端渲染（`static/index.html`）

**`renderSearchResults(data)` 重写**：

- 移除现有「按 tweet_id 去重」逻辑（后端已聚合，无需去重）。
- 对每个 result：`renderCard(r, box)`（复用现有卡片，右侧笔记面板自动就位），然后在 `.card-body` 顶部插入命中列表。
- 命中列表结构（`.search-hit` 换成 `.search-hits` 容器，内含每条 `.search-hit-item`）：
  - `tweet` 命中：只显示一个「微博」来源徽章，不重复正文（正文已全文高亮展示在卡片上）。若该微博同时是 tweet 命中，卡片正文用 `highlight` 高亮后的版本。
  - `comment` / `annotation` 命中：各显示一行，来源徽章 + 完整高亮内容；`comment` 行可点击（`data-doc-id`），触发定位。
- **正文高亮**：微博命中的 `content` 用 `highlight_all` 的结果覆盖卡片正文展示（`renderCard` 用 `t.content`，需在传入前把 `t.content` 替换为高亮版，或加字段 `content_hl`）。为不破坏划线笔记的 `applyHighlights` 偏移定位，正文高亮采用「内容文本不变、仅插入 `<mark>` 标签」——`<mark>` 不改变文本长度，`applyHighlights` 基于文本 offset 的定位仍准确（需在实现时验证）。
- `meta` 行改为：`"「q」 共 N 条微博（Xms）"`，删除「合并同一微博后显示 X 条」提示。

**评论定位（新增 `locateComment(tweetId, docId)`）**：

1. 若评论区未加载，`fetch('/api/tweets/' + tweetId)` 拿到全部评论并渲染（复用 `toggleComments` 的加载逻辑），展开 `.comments-box`。
2. 给评论 DOM 加锚点（实现改动）：`renderComment` 顶层评论 `<div class="comment" data-cid="${c._id}">`；`renderSubCommentInner` 子评论 `<div class="comment-reply" data-cid="${s._id}">`。
3. 定位：`box.querySelector('[data-cid="' + docId + '"]')`；若目标含 `.sub-hidden`（被折叠的子评论），先展开其所在折叠组（复用 `toggleSubs` 逻辑或直接 `classList.remove('sub-hidden')` 并更新按钮文案）。
4. `scrollIntoView({ behavior:'smooth', block:'center' })`，加临时高亮 class `.comment-locate-flash`（CSS 过渡后移除，或 2s 定时移除）。

**CSS 新增**：

```css
.search-hits { margin-bottom: 8px; }
.search-hit-item { font-size: 13px; line-height: 1.6; padding: 4px 0; border-bottom: 1px dashed #eee; }
.search-hit-item[data-doc-id] { cursor: pointer; color: #2b6cb0; }
.search-hit-item[data-doc-id]:hover { background: #f0f7ff; }
.comment-locate-flash { background: #fff3cd; border-radius: 4px; transition: background 2s; }
```

### 4.5 分页

`renderSearchPager` 逻辑不变（`ceil(total / per_page)`），因 `total` 已是去重微博数，页数自然正确。每页稳定 20 条（末页除外），跨页不重复。

## 5. 错误处理

- 命中 doc 补齐失败（doc 已被删的竞态）：静默丢弃该 hit，微博仍在；全部 hit 丢失则丢弃该结果。
- `highlight_all` 对任何 `q`/文本都只做转义 + 标记，无 XSS 面。
- 定位找不到 `[data-cid]`：静默返回，不报错（评论可能在页面渲染后被删除）。

## 6. 测试计划

`tests/test_search.py`：
- `TestSearchAggregation`：同一微博 3 处命中（正文+2 评论）→ `total=1`、单结果 `hits` 长度 3、顺序 tweet→comment→annotation；`total` 等于去重微博数；分页跨页不重复。
- `TestHighlightAll`：完整文本、全词多出现均高亮、无 `q` 时全文本转义、XSS 文本（`<script>`）被转义、空文本返回 `''`。
- `TestHitTextBackfill`：comment hit 的 `highlight` 用 `comments.content` 完整文本；annotation hit 用 `annotations.comment`；tweet hit 用正文；LIKE 路径同样补齐。
- 现有 72 个测试保持通过（含 `TestPicUrlsDeserialization`）。

`tests/test_frontend.py`（字符串断言风格）：
- `renderSearchResults` 不再含去重逻辑、不再含「合并同一微博」文案。
- `locateComment` 存在；`renderComment`/`renderSubCommentInner` 输出含 `data-cid`。
- 搜索结果仍调用 `renderCard(r, box)`。

## 7. 迁移与兼容

- 无 schema 变更，无需重建索引。
- 后端返回结构变化（`results[]` 新增 `hits`、删除顶层 `highlight`/`doc_id`/`source_type` 字段），前端同步改造。
- `make_highlight` 若仅 `search()` 调用则删除，否则保留。

## 8. 范围外

- 不新增评论分页/懒加载（沿用一次性全量加载）。
- 不改 `source_type` 过滤 UI、日期过滤行为。
- 不做 `total` 上限「1000+」优化（上次遗留建议，用户未要求）。
