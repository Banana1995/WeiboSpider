# 定时增量抓取设计

## 目标

将当前手动触发的全量抓取改为定时增量同步：
1. 推文抓取：每小时增量同步（遇已存在推文即停）
2. 评论补齐：每半小时增量补齐（8h 内、<100 条评论的推文，每次抓 2 页热度排序）
3. 手动抓取仍为全量（指定时间区间）

## 背景

### 当前状态
- APScheduler 单 job，每日 02:00 触发，`_crawl()` 全量抓推文 + 评论
- 手动"立即抓取"调用同一个 `_crawl()`
- 评论从第 1 页开始抓，无增量机制
- `get_tweet_ids_with_enough_comments(100)` 跳过已有 ≥100 评论的推文
- 全局单锁，`_running` flag 防止并发

### 关键约束
- 评论必须按热度排序（`flow=0`）抓取
- 热度排序下新评论可能出现在任意排名位置，不能按时间截断
- 单条推文评论总数上限 100 条，达到后永久冻结（不再抓取）
- 推文发布超过 8h 后不再补齐评论

## 设计

### 1. 调度架构

两个独立 APScheduler job，各自独立锁，互不阻塞：

| Job | 触发时间 | 执行内容 |
|-----|---------|---------|
| `tweet_crawl` | 5:00-23:00 每小时 | 增量抓推文（遇已存在推文即停） |
| `comment_crawl` | 5:00-23:00 每半小时 | 增量补齐评论（2 页/推文） |
| 手动"立即抓取" | 用户触发 | 全量抓推文 + 评论（指定时间区间） |
| 手动"增量同步" | 用户触发 | 立即执行一次增量推文 + 增量评论 |

Job 触发时若对应锁被占用，跳过本次（下次自动补上）。两个 job 之间无锁依赖，可并行运行。

### 2. Config 配置项

config 表新增 5 项（均可通过 `/api/config` 读写）：

| Key | 类型 | 默认值 | 说明 |
|-----|------|--------|------|
| `schedule_enabled` | bool | false | 是否启用定时调度 |
| `schedule_start_hour` | int | 5 | 调度起始小时（含） |
| `schedule_end_hour` | int | 23 | 调度结束小时（不含） |
| `tweet_interval_minutes` | int | 60 | 推文抓取间隔（分钟） |
| `comment_interval_minutes` | int | 30 | 评论抓取间隔（分钟） |

调度时间窗外的 job 不触发。例如 `start_hour=5, end_hour=23`：5:00 触发，22:00 是最后一次，23:00 不触发。

### 3. 推文增量抓取（定时模式）

#### Spider 参数

`tweet_by_user_id.py` 新增 `stop_after_id` 参数：
- `_crawl_tweets()` 查询每个 user_id 的最新推文 ID：`SELECT id FROM tweets WHERE user_id=? AND deleted=0 ORDER BY created_at DESC LIMIT 1`
- 作为 `stop_after_id` 传入 spider
- 新用户首次抓取时 `stop_after_id` 为空 → 全量

#### Spider 行为

`parse()` 中检查：
- 遇到 `stop_after_id` 匹配的推文时，仍 yield 该 item（保证 upsert 更新互动数），但不 yield 下一页请求
- `stop_after_id` 为空时，正常翻页直到无更多数据

#### 时间范围

定时模式不传 `start_time`/`end_time`，由 spider 默认行为决定（抓取最新推文）。

### 4. 评论增量抓取（定时模式）

#### 筛选推文

`_crawl_comments()` 查询符合条件的推文：
```sql
SELECT id, mblogid FROM tweets
WHERE deleted = 0
  AND created_at > datetime('now', '-8 hours')
  AND id NOT IN (
    SELECT tweet_id FROM comments
    GROUP BY tweet_id
    HAVING COUNT(*) >= 100
  )
```

#### Spider 参数

`comment.py` 新增 `max_pages` 参数：
- 定时模式传 `max_pages=2`（~100 条/推文，靠 INSERT OR REPLACE 去重）
- 手动模式不传 → 无限制（正常翻页至 100 条上限或无更多数据）

#### Spider 行为

- 保持 `flow=0`（热度排序），从第 1 页开始
- 页数计数器跟踪已请求页数
- 达到 `max_pages` 时停止翻页
- 未达 `max_pages` 但 `max_id == 0`（无更多数据）时正常停止

#### 冻结策略

已有 ≥100 条评论的推文在筛选阶段被排除，永久不再抓取。这是全局策略，定时和手动模式都生效。

### 5. 手动触发

页面提供两个手动按钮：

#### "立即抓取"（全量）
- 推文：不传 `stop_after_id`，全量翻页
- 评论：不传 `max_pages`，正常翻页至 100 条上限或无更多数据
- 使用 config 中的 `start_date` / `end_date` 时间区间
- 仍跳过 ≥100 评论的推文（冻结是全局策略）
- 同时获取两把锁（先推文后评论），任一被占用则拒绝

#### "增量同步"（增量）
- 立即执行一次增量推文 + 增量评论（等价于定时 job 到时间触发一次）
- 推文：传 `stop_after_id`（遇已存在即停）
- 评论：传 `max_pages=2`（2 页热度排序）
- 各自独立锁，互不阻塞（推文和评论可并行执行）
- 锁被占用时拒绝并提示"正在执行中"

### 6. 锁与并发

```
tweet_lock    → 保护 _crawl_tweets()，防同一 job 重叠
comment_lock  → 保护 _crawl_comments()，防同一 job 重叠
```

- 两个锁独立，tweet job 和 comment job 可并行运行
- "立即抓取"（全量）同时获取两把锁（先推文后评论），任一被占用则拒绝并提示"正在抓取中"
- "增量同步"按钮分别获取各自锁，互不阻塞，某把锁被占用时仅拒绝该子任务
- 锁占用时定时 job 触发跳过本次，不排队

### 7. _crawl() 拆分

当前 `_crawl()` 是一个函数同时抓推文和评论。拆为：

```python
def _crawl_tweets(scheduler, mode='incremental', user_id=None):
    """mode: 'incremental' (定时) 或 'full' (手动)"""
    # 写 cookie → 选 user_ids → 按模式传参运行 scrapy
    ...

def _crawl_comments(scheduler, mode='incremental'):
    """mode: 'incremental' (定时) 或 'full' (手动)"""
    # 写 cookie → 筛选推文 → 按模式传参运行 scrapy
    ...
```

手动抓取调用两个函数（顺序执行），定时 job 各调各的，"增量同步"按钮调用两个增量函数（并行执行）。

### 8. 日志与 SSE

- 两个 job 各自写 `crawl.log`，SSE 流不变（tail 同一文件）
- 日志前缀区分：`[tweet]` / `[comment]`
- `/api/crawl/status` 返回两个独立状态：`{tweet: {running, last_result}, comment: {running, last_result}}`

## 代码改动清单

| 文件 | 改动 |
|------|------|
| `scheduler.py` | 两个 job 替代一个；各自独立锁；从 config 读取调度参数；`manual_crawl()` 触发两个全量任务；`manual_incremental()` 触发两个增量任务 |
| `app.py` | `_crawl()` 拆为 `_crawl_tweets(mode)` 和 `_crawl_comments(mode)`；新增 `/api/crawl/incremental` 端点；`/api/crawl/status` 返回双状态 |
| `spiders/tweet_by_user_id.py` | 加 `stop_after_id` 参数 + `parse()` 中停止翻页逻辑 |
| `spiders/comment.py` | 加 `max_pages` 参数 + 页数计数停止逻辑 |
| `db.py` | 加 `get_latest_tweet_id(user_id)` 和 `get_tweets_for_comment_crawl(hours=8)` |
| `index.html` | config 面板加 5 个调度配置项；新增"增量同步"按钮；状态展示适配双状态 |

## 测试

### DB 层（`tests/test_db.py`）
- `get_latest_tweet_id`：返回最新推文 ID；无推文时返回 None
- `get_tweets_for_comment_crawl`：只返回 8h 内、<100 评论、未删除的推文

### Spider 层（`tests/test_common.py` 或新文件）
- tweet spider `stop_after_id` 参数解析
- comment spider `max_pages` 参数解析

### API 层（`tests/test_app.py`）
- `/api/config` 读写 5 个新配置项
- `/api/crawl/status` 返回双状态结构
- `/api/crawl/incremental` 触发增量任务，锁被占用时返回 409

### Scheduler 层（新文件 `tests/test_scheduler.py`）
- job 配置正确性（间隔、时间窗）
- 锁独立性（tweet job 和 comment job 不互相阻塞）
- "立即抓取"占用两把锁
- "增量同步"各获取各自锁

## 非目标

- 不持久化评论游标（`max_id`）——热度排序下游标无意义，每次从第 1 页开始
- 不加 `comment_crawl_time` 列——不参与决策
- 不做请求重试或失败补偿——job 跳过后下次自动补上
- 不改 cookie 处理机制——仍从 DB 读 → 写 cookie.txt → scrapy 读取
