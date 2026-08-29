# 全局搜索功能设计文档

**日期**: 2026-08-29  
**作者**: OpenCode  
**项目**: WeiboSpider 微博管理器  
**版本**: v1.0

---

## 一、需求概述

### 1.1 目标

为 WeiboSpider 添加全局搜索功能，支持对以下内容进行关键词检索：

- 微博正文（tweets.content）
- 转发微博的原文（tweets.retweet_content）
- 抓取的评论内容（comments.content）
- 用户添加的划线笔记（annotations.comment）
- 被划线的原文片段（annotations.selected_text）

**不包含**：博主昵称（已有独立筛选入口，避免噪音）

### 1.2 用户体验模式

**方案 C：工具栏快速搜索 + 独立高级搜索页**

- **快速搜索框**：顶部工具栏添加搜索框，输入关键词后点击搜索或按回车，跳转到搜索结果页
- **高级搜索页**：独立标签页，支持设置时间范围、排序方式、来源类型等过滤条件，显示搜索结果列表

### 1.3 核心决策

| 决策点 | 选择 | 理由 |
|--------|------|------|
| 搜索范围 | 微博正文 + 转发 + 评论 + 笔记（不含昵称） | 覆盖"任意内容"需求，昵称用现有筛选解决 |
| 索引更新时机 | 同步更新（写入时立刻更新索引） | FTS5 触发器开销低（1-2ms/条），实时性最好 |
| 中文分词方案 | Trigram + LIKE 混合 | 纯 SQLite 方案，≥3字用 MATCH，<3字用 LIKE 回退 |

---

## 二、技术方案

### 2.1 技术栈

- **搜索引擎**: SQLite FTS5（Full-Text Search extension）
- **分词器**: `trigram` tokenizer（适合 CJK 语言的子串搜索）
- **运行环境**: 
  - 本地开发: SQLite 3.43.2 + Python 3.9
  - 生产环境: Docker 容器 SQLite 3.46.1（已验证支持 FTS5 + trigram）

### 2.2 性能基准（实测）

在 11 万条模拟中文数据上的测试结果（关键词命中率 0.1%）：

| 查询方式 | 关键词长度 | 耗时 | 是否走索引 |
|---------|-----------|------|-----------|
| FTS5 MATCH | ≥3 字 | **0.1ms** | ✅ 走 trigram 索引 |
| FTS5 LIKE | 任意长度 | 11.5ms | ❌ 全表扫描（LIKE 在 FTS5 上不走索引） |
| 普通表 LIKE | 任意长度 | 8.3ms | ❌ 全表扫描（但比 FTS 表更快） |

**关键发现**（`EXPLAIN QUERY PLAN` 验证）:

1. **LIKE 在 FTS5 表上不走 trigram 索引**：查询计划显示 `SCAN fts VIRTUAL TABLE INDEX 0:L0`（线性扫描），无论关键词长度
2. **FTS5 表的 LIKE 比普通表更慢**：FTS5 虚拟表有额外开销，2 字查询 11.5ms vs 普通表 8.3ms
3. **只有 MATCH 才能利用 trigram 索引**，且要求关键词 ≥3 字符

**结论**: <3 字的查询不应该在 FTS5 表上做 LIKE，而应该直接查业务表（tweets/comments/annotations），因为普通表的 LIKE 更快。

### 2.2.1 真实规模实测（5k 微博 + 11w 评论，2026-08-29 实施验证）

在等量生产数据规模下的实际测量：

| 项目 | 数值 |
|------|------|
| ≥3 字 MATCH 查询（有 trigram 索引） | **0.2-0.4ms**（目标 <100ms ✅） |
| 2 字 LIKE 查询（业务表扫描） | **6-18ms**（目标 <500ms ✅） |
| 批量写入索引同步（每行 2 次 executemany） | **77k rows/s**（20k 条混合新+更新 0.26s） |
| 数据库文件大小（5k 微博 + 11w 评论） | ~12MB |

**关键发现：FTS5 按列删除是 O(index_size)**

初版用 `DELETE FROM search_index WHERE doc_id=? AND source_type=?` 做 upsert，实测该删除在 trigram 表上是**线性扫描**（按非 rowid 列删除无法用索引）：

| 索引规模 | 删除 20k 行耗时 |
|----------|----------------|
| 5k 行 | ~9s |
| 10k 行 | ~17.4s |
| 114k 行（生产） | **~16 分钟**（外推） |

**修复**：新增普通表 `search_doc(doc_id, source_type, fts_rowid, PRIMARY KEY(doc_id, source_type))` 映射 doc→FTS5 rowid。删除先查此表拿 rowid，再 `DELETE FROM search_index WHERE rowid=?`（O(1)）。修复后同一场景 **77k rows/s，且不随索引规模退化**。

**升级兼容**：`_backfill_search_index` 用 `INSERT OR IGNORE` 填充 `search_doc`（老代码可能产生重复 (doc_id, source_type)，直接 INSERT 会 UNIQUE 冲突导致启动崩溃——已修复并加测试覆盖）。

### 2.3 BM25 排序的限制

**实测发现**: Trigram 分词器下 `bm25()` 返回的分数全部为 `-0.0000`，**无法提供有意义的相关性排序**。

原因：trigram 把文本切成大量重叠的 3-gram tokens，导致所有文档的 token 分布趋同，BM25 的 IDF 权重失去区分度。

**影响**: 搜索结果无法按"相关度"排序。

**应对**: 
- 默认按**时间倒序**排序（最新的在前），这对微博场景更符合直觉
- 提供"按来源类型分组"的展示方式（先显示微博正文匹配，再显示评论匹配，最后是笔记匹配）
- 不提供"相关度排序"选项（避免给用户虚假承诺）

### 2.4 混合查询策略（修正版）

```python
def search(keyword, ...):
    if len(keyword) >= 3:
        # 路径 A: FTS5 MATCH（走 trigram 索引，快）
        sql = """
            SELECT doc_id, source_type, tweet_id, 
                   snippet(search_index, 3, '<mark>', '</mark>', '...', 12) AS highlight
            FROM search_index 
            WHERE search_index MATCH ?
        """
        params = [f'"{escape_fts5(keyword)}"']  # 用引号包裹，避免 FTS5 语法注入
    else:
        # 路径 B: 直接查业务表 LIKE（普通表扫描比 FTS5 表更快）
        # 分别查 tweets / comments / annotations，UNION ALL 后统一处理
        sql = """
            SELECT id AS doc_id, 'tweet' AS source_type, id AS tweet_id, content AS text
            FROM tweets WHERE deleted=0 AND (content LIKE ? OR retweet_content LIKE ?)
            UNION ALL
            SELECT c.id, 'comment', c.tweet_id, c.content
            FROM comments c WHERE c.content LIKE ?
            UNION ALL
            SELECT a.id, 'annotation', a.tweet_id, a.comment
            FROM annotations a WHERE a.comment LIKE ? OR a.selected_text LIKE ?
        """
        like = f'%{keyword}%'
        params = [like] * 5
        # 高亮由 Python 端处理（业务表查询无 snippet 函数）
```

**权衡说明**:
- ≥3 字（覆盖大部分搜索场景）: 走索引，亚毫秒响应，有 snippet 高亮
- <3 字: 全表扫描，当前数据量下约 10-30ms，可接受；高亮在 Python 端做

---

## 三、数据层设计

### 3.1 FTS5 索引表结构

新建统一搜索索引表 `search_index`:

```sql
CREATE VIRTUAL TABLE search_index USING fts5(
    doc_id,         -- 唯一标识符（tweet_id / comment_id / annotation_id）
    source_type,    -- 来源类型：'tweet' | 'comment' | 'annotation'
    tweet_id,       -- 所属微博 ID（用于结果聚合和跳转）
    text,           -- 被索引的文本内容
    tokenize='trigram'
);
```

**字段说明**:

| 字段 | 类型 | 说明 |
|------|------|------|
| doc_id | TEXT | 源表的主键 ID（区分具体是哪一条记录） |
| source_type | TEXT | 'tweet' / 'comment' / 'annotation' |
| tweet_id | TEXT | 关联微博 ID（用于结果页跳转和去重） |
| text | TEXT | 实际被索引的文本（单字段或拼接字段） |

**技术说明**: 
- 所有字段都参与索引（移除 UNINDEXED），以支持 `WHERE source_type=?` 等过滤条件
- Trigram 分词器会对所有文本字段生成 3-gram tokens

### 3.2 数据来源映射

| source_type | doc_id 来源 | tweet_id 来源 | text 内容 |
|-------------|------------|--------------|----------|
| tweet | tweets.id | tweets.id | tweets.content + ' ' + tweets.retweet_content（拼接） |
| comment | comments.id | comments.tweet_id | comments.content |
| annotation | annotations.id | annotations.tweet_id | annotations.comment + ' ' + annotations.selected_text |

**设计考量**:

- **一条微博一条索引记录**: 正文和转发内容拼接成一条 `text`，避免一条微博拆成多条记录导致结果重复
- **评论和笔记独立**: 每条评论、每条笔记各占一条索引记录，便于定位具体匹配位置
- **空格分隔**: 拼接字段间用空格分隔，防止边界粘连（如 `content='股票'` + `retweet_content='市场'` 拼成 `'股票市场'` 会误匹配）

### 3.3 索引维护策略

**同步更新模式**: 所有写操作（爬虫抓取 / 用户编辑笔记）在写入业务表后立刻同步更新 `search_index`。

#### 3.3.1 写入路径

| 业务表 | 写入入口（db.py） | 索引操作 |
|--------|------------------|----------|
| tweets | insert_tweet / upsert_tweet / batch_insert_tweets | INSERT OR REPLACE INTO search_index |
| comments | insert_comment / batch_insert_comments | INSERT OR REPLACE INTO search_index |
| annotations | insert_annotation | INSERT INTO search_index |
| annotations | update_annotation | UPDATE search_index SET text=? WHERE doc_id=? AND source_type='annotation' |
| annotations | delete_annotation | DELETE FROM search_index WHERE doc_id=? AND source_type='annotation' |

#### 3.3.2 初始化逻辑

在 `db.py` 的 `_create_tables()` 方法中：

1. 创建 `search_index` 虚拟表（如果不存在）
2. 检查表是否为空：`SELECT COUNT(*) FROM search_index`
3. 如果为空且业务表有数据，执行全量初始化：
   ```sql
   -- 插入所有微博
   INSERT INTO search_index(doc_id, source_type, tweet_id, text)
   SELECT id, 'tweet', id, content || ' ' || COALESCE(retweet_content, '')
   FROM tweets WHERE deleted=0;
   
   -- 插入所有评论
   INSERT INTO search_index(doc_id, source_type, tweet_id, text)
   SELECT id, 'comment', tweet_id, content FROM comments;
   
   -- 插入所有笔记
   INSERT INTO search_index(doc_id, source_type, tweet_id, text)
   SELECT id, 'annotation', tweet_id, comment || ' ' || selected_text
   FROM annotations;
   ```

#### 3.3.3 并发控制

- 复用现有 `db.py` 的跨进程文件锁机制（`_acquire_file_lock` / `_release_file_lock`）
- SQLite WAL 模式（已启用）允许读并发，写操作串行化，无需额外改动

---

## 四、API 设计

### 4.1 搜索接口

**端点**: `GET /api/search`

**请求参数**:

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| q | string | 是 | - | 搜索关键词 |
| page | int | 否 | 1 | 页码（从 1 开始） |
| per_page | int | 否 | 20 | 每页条数（1-100） |
| source_type | string | 否 | all | 来源筛选：all / tweet / comment / annotation |
| start_date | string | 否 | - | 开始日期（YYYY-MM-DD，根据 tweets.created_at 筛选） |
| end_date | string | 否 | - | 结束日期（YYYY-MM-DD） |
| sort | string | 否 | time | 排序方式：time（时间倒序，默认）/ grouped（按来源分组：微博→评论→笔记） |

**响应格式**:

```json
{
  "results": [
    {
      "tweet_id": "4982...",
      "source_type": "tweet",
      "doc_id": "4982...",
      "highlight": "...关键词高亮...",
      "tweet": {
        "id": "4982...",
        "content": "...",
        "created_at": "2024-08-15T10:30:00",
        "user_id": "123456",
        "screen_name": "张三"
      },
      "match_info": {
        "matched_in": "content",  // 或 "annotation" / "comment"
        "comment_id": "xyz...",   // 如果是评论匹配
        "annotation_id": "abc..." // 如果是笔记匹配
      }
    }
  ],
  "total": 42,
  "page": 1,
  "per_page": 20,
  "query": "股票",
  "elapsed_ms": 12.5
}
```

**字段说明**:

- `results`: 搜索结果列表，每条结果附带完整的微博信息（便于前端直接渲染卡片）
- `highlight`: 命中关键词的上下文片段，关键词用 `<mark>` 标签包裹
- `match_info`: 额外匹配信息（用于前端跳转到具体评论或笔记位置）
- `elapsed_ms`: 后端执行耗时（用于性能监控）

### 4.2 SQL 查询逻辑

**两条查询路径**，由关键词长度决定（见 2.4 节的实测依据）。

#### 路径 A：关键词 ≥3 字 —— FTS5 MATCH（走 trigram 索引）

```python
# FTS5 MATCH 必须整表匹配（search_index MATCH ?），不能写 column MATCH ?
sql = """
SELECT si.doc_id, si.source_type, si.tweet_id,
       snippet(search_index, 3, '<mark>', '</mark>', '…', 12) AS highlight,
       t.id, t.content, t.created_at, t.user_id, t.screen_name, t.platform
FROM search_index si
JOIN tweets t ON t.id = si.tweet_id
WHERE search_index MATCH ?
  AND t.deleted = 0
  {extra_filters}          -- source_type / 时间范围
ORDER BY t.created_at DESC  -- 不用 bm25()，trigram 下无区分度
LIMIT ? OFFSET ?
"""
params = [fts5_quote(q), *filter_params, per_page, offset]
```

**注意事项**（均已实测验证）:

1. **MATCH 的左值必须是表名**，不能是列名。`WHERE search_index.text MATCH ?` 会报错，正确写法是 `WHERE search_index MATCH ?`
2. **关键词必须转义**：用户输入可能含 FTS5 保留字符（`"` `*` `:` `(` `)` `AND` `OR` `NOT`），必须用双引号包裹成短语查询并转义内部引号，否则会被当作查询语法解析（既是 bug 也是注入风险）：
   ```python
   def fts5_quote(s):
       return '"' + s.replace('"', '""') + '"'
   ```
3. **snippet 的列索引是 3**（对应 `text` 列，从 0 开始数：doc_id=0, source_type=1, tweet_id=2, text=3）
4. **snippet 要求表存储内容**：不能用 `content=''` 的 contentless 模式（实测该模式下 snippet 返回 `None`，且无法读取列值、无法按列过滤）

#### 路径 B：关键词 <3 字 —— 直接查业务表 LIKE

不查 `search_index`（实测 FTS5 表的 LIKE 比普通表慢），直接 UNION ALL 三张业务表：

```python
sql = """
SELECT id AS doc_id, 'tweet' AS source_type, id AS tweet_id, content AS matched_text
  FROM tweets WHERE deleted=0 AND (content LIKE ? OR retweet_content LIKE ?)
UNION ALL
SELECT id, 'comment', tweet_id, content
  FROM comments WHERE content LIKE ?
UNION ALL
SELECT id, 'annotation', tweet_id, comment
  FROM annotations WHERE comment LIKE ? OR selected_text LIKE ?
"""
```

再在外层 JOIN `tweets` 补全展示信息、套时间/来源过滤、分页。

**高亮**: 业务表查询没有 `snippet()`，在 Python 端生成 —— 截取命中位置前后各 20 字，用 `<mark>` 包裹关键词，并对原文做 HTML 转义后再插入标签（防 XSS）。

#### 两条路径的统一

两条路径返回相同的字段结构（`doc_id` / `source_type` / `tweet_id` / `highlight` / 微博展示字段），Flask 层统一封装成 4.1 节的响应格式，前端无需区分。

---

## 五、前端设计

### 5.1 工具栏快速搜索

**位置**: 顶部工具栏（`.toolbar`）右侧，"导出 PDF" 按钮之前

**交互**:
1. 输入框输入关键词
2. 点击搜索图标或按 Enter 键
3. 切换到"搜索结果"标签页，URL 变更为 `?tab=search&q=关键词`
4. 加载搜索结果

**HTML 结构**:

```html
<div class="search-box">
  <input type="text" id="quick-search-input" placeholder="搜索微博、评论、笔记...">
  <button id="quick-search-btn">🔍</button>
</div>
```

**样式要点**:
- 响应式宽度: 桌面端 280px，平板 200px，手机 100%
- 与工具栏其他按钮对齐
- 输入框聚焦时边框高亮（`border-color: #4a9eff`）

### 5.2 搜索结果页（独立标签页）

**标签页标题**: "搜索"（tabs 数组新增一项）

**布局结构**:

```
┌─────────────────────────────────────┐
│ [🔍 关键词输入框]  [搜索按钮]         │  ← 搜索栏
├─────────────────────────────────────┤
│ 高级筛选: [来源▼] [时间范围] [排序▼] │  ← 筛选栏（可折叠）
├─────────────────────────────────────┤
│ 找到 42 条结果（12.5ms）              │  ← 统计行
├─────────────────────────────────────┤
│ ┌─ 微博卡片 ───────────────────┐    │
│ │ 张三 · 2024-08-15 10:30       │    │
│ │ ...关键词高亮片段...          │    │
│ │ [展开全文] [查看评论]          │    │
│ └───────────────────────────────┘    │
│ ┌─ 评论卡片 ───────────────────┐    │
│ │ 所属微博: #4982... (点击跳转)  │    │
│ │ 李四: ...关键词高亮片段...     │    │
│ └───────────────────────────────┘    │
│ ...                                  │
├─────────────────────────────────────┤
│         [上一页] 1 / 3 [下一页]      │  ← 分页
└─────────────────────────────────────┘
```

**卡片渲染逻辑**:

- **微博匹配**: 复用现有的 `renderCard()` 函数，在卡片顶部添加 `highlight` 摘要（折叠显示）
- **评论匹配**: 渲染为简化卡片，包含：
  - 所属微博标题（可点击跳转到微博详情页并自动展开评论区）
  - 评论作者和高亮内容
  - 时间和点赞数
- **笔记匹配**: 渲染为笔记卡片，包含：
  - 所属微博标题
  - 被划线的原文片段（`selected_text`）
  - 笔记内容（`comment`）
  - 点击跳转到微博详情页并定位到该笔记

### 5.3 高亮显示

**关键词高亮**: 后端返回的 `highlight` 字段已包含 `<mark>` 标签，前端直接渲染：

```javascript
cardBody.innerHTML = `<div class="search-highlight">${result.highlight}</div>`;
```

**CSS 样式**:

```css
.search-highlight mark {
  background: #fff3cd;
  color: #856404;
  padding: 1px 3px;
  border-radius: 2px;
  font-weight: 600;
}
```

### 5.4 空状态和错误处理

**无结果**:
```
🔍 未找到包含 "关键词" 的内容

尝试：
- 检查拼写
- 使用更通用的词
- 减少关键词数量
```

**搜索失败**:
```
❌ 搜索出错，请稍后重试
[错误详情]
```

---

## 六、实现计划

### 6.1 任务分解

| 阶段 | 任务 | 文件 | 预计工作量 |
|------|------|------|-----------|
| 1. 数据层 | 创建 FTS5 索引表 | db.py | 30 分钟 |
| 1. 数据层 | 在现有写入方法中同步更新索引 | db.py | 1 小时 |
| 1. 数据层 | 全量初始化逻辑 | db.py | 30 分钟 |
| 2. API 层 | 实现 `/api/search` 接口 | app.py | 1.5 小时 |
| 2. API 层 | 添加搜索结果高亮和分页 | app.py | 30 分钟 |
| 3. 前端 | 工具栏快速搜索框 | index.html | 30 分钟 |
| 3. 前端 | 搜索结果页标签页和布局 | index.html | 1 小时 |
| 3. 前端 | 结果卡片渲染（微博/评论/笔记） | index.html | 1.5 小时 |
| 3. 前端 | 高级筛选和分页交互 | index.html | 1 小时 |
| 4. 测试 | 单元测试（DB 层） | tests/test_search.py | 1 小时 |
| 4. 测试 | API 测试 | tests/test_app.py | 30 分钟 |
| 4. 测试 | 前端手动测试和调优 | - | 1 小时 |

**总计**: 约 10.5 小时

### 6.2 里程碑

1. **M1: 数据层完成**（2 小时）
   - 索引表创建和初始化成功
   - 现有数据全部索引完毕
   - 写入路径验证通过

2. **M2: API 可用**（+2 小时，累计 4 小时）
   - `/api/search` 返回正确结果
   - 高亮和分页正常工作
   - Trigram/LIKE 混合逻辑验证

3. **M3: 前端完成**（+4 小时，累计 8 小时）
   - 搜索框和结果页 UI 完成
   - 三种卡片类型渲染正确
   - 筛选和排序交互正常

4. **M4: 测试通过**（+2.5 小时，累计 10.5 小时）
   - 单元测试覆盖核心逻辑
   - 边界情况验证（空查询、特殊字符、大量结果）
   - 性能基准测试

---

## 七、风险和缓解

### 7.1 性能风险

**风险**: 数据量增长到 50 万条后，LIKE 查询可能变慢（目前 11 万条 ~10ms）

**缓解**:
- 短期: 限制 LIKE 查询结果集（最多返回 1000 条，按时间倒序）
- 中期: 添加 SQL EXPLAIN QUERY PLAN 监控，发现慢查询后优化
- 长期: 数据量达到 50 万时考虑升级到 jieba 分词或 Meilisearch

### 7.2 中文分词限制

**风险**: 单字查询（如"好"、"涨"）会返回大量无关结果（trigram 无法索引单字，回退到全表 LIKE）

**缓解**:
- 前端提示: 搜索框下方显示"建议使用 2 个字以上的关键词"
- 后端限制: 单字查询最多返回 100 条结果
- 未来优化: 如确实需要单字查询，可考虑引入 jieba 分词（需评估 Docker 镜像体积增加）

### 7.3 索引维护失败

**风险**: 爬虫批量写入时如果某条记录索引失败，可能导致部分内容搜不到

**缓解**:
- 所有索引更新用 try-except 包裹，失败时只记录日志不中断业务写入
- 提供手动"重建索引"接口（`POST /api/search/rebuild`），管理员可触发全量重建
- 定期巡检: 每周对比 `COUNT(*)` 差异（`tweets` vs `search_index WHERE source_type='tweet'`）

---

## 八、未来扩展

### 8.1 短期优化（3 个月内）

- **搜索历史**: 本地存储最近 10 条搜索记录，快速重搜
- **热门搜索**: 统计高频关键词，首页展示"大家都在搜"
- **导出搜索结果**: 支持将搜索结果导出为 PDF（复用现有导出逻辑）

### 8.2 长期规划（6-12 个月）

- **多关键词 AND/OR 查询**: 支持 `"股票 AND 大涨"` 或 `"基金 OR 理财"` 语法
- **日期范围语法**: 支持 `"创业板 date:2024-08"` 快捷语法
- **全文预览**: 点击搜索结果后在弹窗中预览完整微博（不跳转），类似 Slack 的搜索体验
- **AI 语义搜索**: 集成 Embedding 模型（如 BGE-M3），支持"找相似内容"功能

---

## 九、附录

### 9.1 参考资料

- [SQLite FTS5 官方文档](https://www.sqlite.org/fts5.html)
- [Trigram Tokenizer 说明](https://www.sqlite.org/fts5.html#the_trigram_tokenizer)
- [FTS5 snippet() 函数](https://www.sqlite.org/fts5.html#the_snippet_function)

### 9.2 测试数据集

在 `tests/fixtures/` 目录下准备测试数据：

- `test_tweets.json`: 100 条微博（包含中文、英文、emoji、特殊字符）
- `test_comments.json`: 500 条评论
- `test_annotations.json`: 50 条笔记

用于验证：
- 中文分词准确性
- 特殊字符转义
- 边界情况（空内容、超长内容）

### 9.3 性能基准目标

| 场景 | 目标响应时间 | 测试方法 |
|------|-------------|---------|
| 3 字及以上关键词（MATCH） | < 50ms | 在 11 万条数据上执行 100 次查询取平均值 |
| 2 字关键词（LIKE） | < 100ms | 同上 |
| 索引更新（单条） | < 5ms | 批量插入 1000 条测量平均值 |
| 全量重建索引 | < 10 秒 | 在 11 万条数据上执行 |

---

**设计文档完成**。请审阅后告知是否继续进入实现计划阶段。
