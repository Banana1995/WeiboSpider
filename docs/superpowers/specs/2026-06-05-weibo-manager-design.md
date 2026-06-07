# 微博内容管理器 - 设计文档

## 概述

基于 nghuyong/WeiboSpider 项目改造，实现针对单个微博博主的全量内容抓取 + Web 端批量管理删除。

## 需求摘要

1. 每天全量抓取指定博主的微博内容和评论
2. 抓取支持自动（凌晨2点）+ 手动触发
3. Web 界面瀑布流展示微博卡片（含评论），勾选后批量软删除
4. 评论默认折叠，点击展开
5. 尽可能简单：单进程、零前端构建、SQLite

## 技术选型

| 层 | 技术 | 理由 |
|---|------|------|
| 抓取 | 复用现有 Scrapy spiders | tweet_by_user_id + comment，改造为写入 SQLite |
| 后端 | Flask + 内置 server | 轻量，REST API + 静态文件服务 |
| 前端 | Vanilla JS 单页 | 零构建、零框架，约400行 HTML/JS |
| 数据库 | SQLite | 零部署，单文件 |
| 定时 | APScheduler | Python 内调度，与 Flask 同进程 |

## 项目结构

```
weibospider/
├── spiders/              # 现有spiders（改造：写入SQLite而非JSONL）
├── db.py                 # 新增：SQLite操作封装
├── app.py                # 新增：Flask API + 静态文件服务
├── static/
│   └── index.html        # 新增：SPA单页
├── scheduler.py          # 新增：APScheduler 定时调度
├── run.py                # 新增：统一启动入口（web + scheduler）
├── cookie.txt            # 保留：微博登录Cookie
├── middlewares.py        # 保留：代理中间件
├── settings.py           # 保留：Scrapy配置
└── pipelines.py          # 废弃/改造：不再写JSONL
```

## 数据模型

### tweets 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | TEXT PK | 微博 mid |
| mblogid | TEXT | 短ID |
| user_id | TEXT NOT NULL | 博主UID |
| content | TEXT NOT NULL | 微博正文 |
| created_at | TEXT | 发布时间 |
| reposts_count | INTEGER | 转发数 |
| comments_count | INTEGER | 评论数 |
| attitudes_count | INTEGER | 点赞数 |
| pic_urls | TEXT | 图片URL列表(JSON) |
| pic_num | INTEGER | 图片数量 |
| source | TEXT | 发布设备 |
| ip_location | TEXT | IP属地 |
| is_retweet | INTEGER | 是否转发 |
| retweet_id | TEXT | 转发原微博ID |
| url | TEXT | 微博链接 |
| crawl_time | INTEGER | 抓取时间戳 |
| deleted | INTEGER | 软删除标记(0=正常, 1=已删除) |
| deleted_at | TEXT | 删除时间 |

### comments 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | TEXT PK | 评论ID |
| tweet_id | TEXT NOT NULL | 所属微博mid(FK) |
| content | TEXT NOT NULL | 评论内容 |
| created_at | TEXT | 评论时间 |
| like_counts | INTEGER | 点赞数 |
| ip_location | TEXT | 评论者IP属地 |
| comment_user | TEXT | 评论者信息(JSON) |
| reply_comment | TEXT | 子回复(JSON) |
| crawl_time | INTEGER | 抓取时间戳 |

### 索引

```sql
CREATE INDEX idx_tweets_user_id ON tweets(user_id);
CREATE INDEX idx_tweets_created_at ON tweets(created_at);
CREATE INDEX idx_tweets_deleted ON tweets(deleted);
CREATE INDEX idx_comments_tweet_id ON comments(tweet_id);
```

### 去重策略

微博id和评论id作为主键，使用 `INSERT OR IGNORE` 写入，已存在的数据自动跳过。

### 删除策略

软删除：设置 `deleted = 1` 和 `deleted_at` 时间戳。前端默认查询 `WHERE deleted = 0`。提供回收站视图查看已删除内容，支持恢复。

## API 设计

| Method | Path | 说明 |
|--------|------|------|
| GET | `/api/tweets` | 分页查询微博（排除已删除），参数: page, per_page, sort |
| GET | `/api/tweets/<id>` | 单条微博 + 其评论列表 |
| DELETE | `/api/tweets/batch-delete` | 批量软删除，Body: {"ids": [...]} |
| DELETE | `/api/tweets/<id>` | 删除单条 |
| POST | `/api/crawl` | 手动触发全量抓取，异步执行 |
| GET | `/api/crawl/status` | 查询抓取任务状态 |
| GET | `/api/stats` | 统计: 总数、已删、评论数 |
| GET | `/` | 返回 index.html SPA |

## 前端设计

### 布局

- 顶部操作栏：统计数字 + "立即抓取"按钮 + "删除选中(N)"按钮
- 主体瀑布流：微博卡片纵向排列，无限滚动加载
- 每张卡片包含：
  - 勾选框（左上角）
  - 单条删除按钮（右上角 ✕）
  - 时间和来源信息
  - 微博正文
  - 图片展示（如有）
  - 转发/点赞数
  - **评论按钮**（默认折叠）：显示"💬 N 条评论 ▼"，点击展开/收起

### 交互

- 全选/反选：顶部 checkbox 一键全选当前卡片
- 无限滚动：滚动到底部自动加载下一页
- 批量删除：选中 → "删除选中" → 确认 → AJAX → 卡片即时消失
- 单条删除：卡片右上角 ✕ → 确认 → 即时消失
- 回收站视图：切换查看已删除内容、支持恢复
- 抓取状态：进行中显示进度、禁用重复触发

## 调度方案

- APScheduler 集成在 Flask 进程中
- Cron 触发器：每天 02:00 执行
- 手动触发通过 POST /api/crawl
- 互斥锁防止并发抓取

### 抓取流程

1. 触发（定时/手动）
2. tweet_by_user_id spider 抓取所有微博 → INSERT OR IGNORE
3. 对每条新微博，comment spider 抓取评论 → INSERT OR IGNORE
4. 更新状态，返回摘要（新增N条，跳过M条）

## 依赖

```
# 新增
Flask==2.3
APScheduler==3.10

# 保留
Scrapy==2.5.1
python_dateutil
cryptography==36.0.2
pyOpenSSL==22.0.0
Twisted==22.10.0
```

## 配置项

运行前需要配置：
- `cookie.txt`：微博登录 Cookie（需用户从浏览器复制）
- `weibospider/spiders/tweet_by_user_id.py` 中的 `user_ids`：目标博主 UID
