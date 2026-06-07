# 微博管理器

基于 Scrapy + Flask 的微博内容抓取与管理系统，支持定时抓取、热度排序评论、PDF 导出。

## 功能特性

- **定时抓取**：每日凌晨 2:00 自动抓取关注博主的微博及评论
- **热度评论**：按微博官方热度排序抓取评论，本地保持相同排序
- **实时日志**：抓取过程日志通过 SSE 实时推送到前端
- **PDF 导出**：一键导出微博内容为 PDF，嵌入中文字体，中文完美显示
- **批量管理**：支持批量删除/恢复微博
- **Web 管理界面**：SPA 单页应用，瀑布流卡片展示

## 环境要求

- Python 3.8+
- Google Chrome（PDF 导出功能需要）
- macOS / Linux

## 安装

```bash
# 克隆仓库
git clone https://github.com/Banana1995/WeiboSpider.git
cd WeiboSpider

# 创建虚拟环境（推荐）
python3 -m venv venv
source venv/bin/activate

# 安装依赖
pip install -r requirements.txt
```

## 配置

### 1. 获取微博 Cookie

1. 用 Chrome 浏览器打开 [weibo.com](https://weibo.com) 并登录
2. 按 F12 打开开发者工具 → Application → Cookies → https://weibo.com
3. 将所有 Cookie 拼接成字符串（格式：`key1=value1; key2=value2; ...`）

### 2. 首次启动

首次启动时会自动生成数据库和默认配置。启动后通过 Web 界面配置：

- **Cookie**：粘贴你获取的 Cookie 字符串
- **用户 UID**：添加你要监控的博主 UID（在博主主页 URL 中可找到）
- **时间范围**：设置抓取微博的时间范围（留空 = 不限时间）

## 启动服务

```bash
cd weibospider

# 开发模式（改代码自动热更新，默认端口 5000）
python run.py --dev

# 生产模式（多线程，更稳定）
python run.py

# 指定端口
python run.py --port 8080
```

或使用脚本后台启动：

```bash
./start.sh              # 后台启动
./start.sh --port 8080  # 指定端口
./stop.sh               # 停止服务
```

启动后访问 http://localhost:5000

## 使用指南

### 抓取微博

1. 在界面中配置好 Cookie 和要监控的博主 UID
2. 设置时间范围（可选，留空表示不限时间）
3. 点击 **"立即抓取"** 按钮
4. 查看实时日志了解抓取进度
5. 抓取完成后页面自动刷新显示新数据

系统也会每天凌晨 2:00 自动执行一次抓取。

### 查看评论

- 每条微博卡片底部显示评论数
- 点击 **"评论 N"** 展开查看热度排序的评论
- 热度排序使用微博官方 `flow=0` 接口，按综合热度降序排列

### 导出 PDF

1. 点击工具栏 **"导出 PDF"** 按钮
2. 在弹出的模态框中选择导出时间范围（留空 = 全部）
3. 点击 **"确认导出"**
4. 浏览器自动下载 PDF 文件

PDF 使用嵌入的 Noto Sans SC 中文字体，适合打印和存档。

### 管理微博

- **删除**：点击卡片右上角 × 按钮，微博进入回收站
- **批量删除**：勾选多条微博后点击 "删除选中"
- **恢复**：切换到 "回收站" 标签，勾选后点击 "撤回选中"
- **按博主筛选**：点击配置区的博主名，只看该博主的微博

## 项目结构

```
WeiboSpider/
├── weibospider/
│   ├── app.py              # Flask Web 应用、API 路由、PDF 导出
│   ├── db.py               # SQLite 数据库操作
│   ├── run.py              # 启动入口
│   ├── scheduler.py        # 定时抓取调度器
│   ├── settings.py         # Scrapy 全局配置
│   ├── pipelines.py        # Scrapy 数据管道
│   ├── middlewares.py      # Scrapy 中间件（代理、UA）
│   ├── start.sh / stop.sh  # 后台启动/停止脚本
│   ├── static/
│   │   └── index.html      # 前端 SPA 页面
│   └── spiders/
│       ├── tweet_by_user_id.py  # 微博抓取爬虫
│       ├── comment.py           # 评论抓取爬虫（热度排序）
│       └── common.py            # 公共工具函数
├── requirements.txt
└── .gitignore
```

## API 说明

| 端点 | 方法 | 说明 |
|------|------|------|
| `/api/tweets` | GET | 获取微博列表（支持分页、筛选、回收站） |
| `/api/tweets/<id>` | GET | 获取单条微博及评论 |
| `/api/tweets/<id>` | DELETE | 删除微博（软删除，进入回收站） |
| `/api/tweets/batch-delete` | POST | 批量删除 |
| `/api/tweets/batch-restore` | POST | 批量恢复 |
| `/api/export` | GET | 导出数据（`?format=pdf` 下载 PDF，`?start=&end=` 筛选时间） |
| `/api/crawl` | POST | 手动触发抓取（可选 `{"user_id":"xxx"}` 抓取指定用户） |
| `/api/crawl/cancel` | POST | 取消正在进行的抓取 |
| `/api/crawl/events` | GET | SSE 端点，推送实时日志和抓取状态 |
| `/api/config` | GET/POST | 读取/修改配置（cookie, user_ids, start_date, end_date） |
| `/api/stats` | GET | 获取数据统计（微博总数、评论总数等） |

## 技术栈

- **爬虫框架**：Scrapy 2.5
- **Web 框架**：Flask 2.3 + Waitress
- **定时任务**：APScheduler 3.10
- **数据库**：SQLite
- **PDF 生成**：Headless Chrome + Google Fonts (Noto Sans SC)
- **实时通信**：Server-Sent Events (SSE)

## License

MIT
