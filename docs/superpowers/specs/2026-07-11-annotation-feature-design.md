# 划线评论功能 - 设计文档

## 概述

在微博管理器的微博列表界面，支持用户选中文本进行划线高亮，添加评论。评论面板在右侧同行位置展示，随页面一起滚动。

## 需求确认

- **划线范围**：页面上全部文本（微博正文、转发内容、评论等）
- **持久化**：划线评论保存到数据库，刷新/重启后仍在
- **评论归属**：绑定到具体微博，每条微博有独立的划线评论
- **编辑删除**：支持编辑和删除划线评论
- **布局**：左栏微博卡片 + 右栏评论面板，同行排列，一起滚动
- **交互**：选中文字 → 右侧同行位置出现输入框 → 保存后原地显示评论卡片
- **定位方案**：字符偏移量（start_offset + end_offset + selected_text 校验）

## 数据模型

### annotations 表

| 字段 | 类型 | 说明 |
|------|------|------|
| id | TEXT PK | UUID |
| tweet_id | TEXT NOT NULL | 关联的微博 ID (FK → tweets.id) |
| start_offset | INTEGER NOT NULL | 选中文本起始字符偏移量 |
| end_offset | INTEGER NOT NULL | 选中文本结束字符偏移量 |
| selected_text | TEXT NOT NULL | 选中的文字内容（校验用） |
| comment | TEXT NOT NULL | 评论内容 |
| field | TEXT DEFAULT 'content' | 划线所在字段（content / retweet_content 等） |
| created_at | TEXT | 创建时间 |
| updated_at | TEXT | 更新时间 |

```sql
CREATE TABLE IF NOT EXISTS annotations (
    id          TEXT PRIMARY KEY,
    tweet_id    TEXT NOT NULL,
    start_offset INTEGER NOT NULL,
    end_offset   INTEGER NOT NULL,
    selected_text TEXT NOT NULL,
    comment     TEXT NOT NULL,
    field       TEXT DEFAULT 'content',
    created_at  TEXT,
    updated_at  TEXT,
    FOREIGN KEY (tweet_id) REFERENCES tweets(id)
);
CREATE INDEX IF NOT EXISTS idx_annotations_tweet_id ON annotations(tweet_id);
```

### field 字段说明

用户可以对"全部文本"划线，需要区分划线在哪个文本区域：

- `content`：微博正文（.card-content）
- `retweet_content`：转发的原博内容（.retweet-block 内文本）
- `comment`：评论内容（.comment 内文本）

定位时根据 `field` 找到对应的 DOM 元素，再在该元素的文本中用 offset 定位。

## API 设计

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/tweets/<tweet_id>/annotations` | 获取该微博的所有划线评论 |
| POST | `/api/tweets/<tweet_id>/annotations` | 新建划线评论 |
| PUT | `/api/annotations/<id>` | 编辑评论内容 |
| DELETE | `/api/annotations/<id>` | 删除划线评论 |

### 请求/响应格式

**POST `/api/tweets/<tweet_id>/annotations`**
```json
{
  "start_offset": 15,
  "end_offset": 28,
  "selected_text": "美伊核协议",
  "comment": "这个协议最后还是回到了原点",
  "field": "content"
}
```

**GET `/api/tweets/<tweet_id>/annotations` 响应**
```json
[
  {
    "id": "uuid-xxx",
    "tweet_id": "5309362370512360",
    "start_offset": 15,
    "end_offset": 28,
    "selected_text": "美伊核协议",
    "comment": "这个协议最后还是回到了原点",
    "field": "content",
    "created_at": "2026-07-11 15:00:00",
    "updated_at": "2026-07-11 15:00:00"
  }
]
```

**PUT `/api/annotations/<id>`**
```json
{ "comment": "修改后的评论内容" }
```

## 前端设计

### 布局

- `#app` 最大宽度从 700px 改为 ~1000px
- 微博列表区域改为双栏 flex 布局：
  - 左栏：微博卡片，最大宽度 ~680px
  - 右栏：评论面板，宽度 ~260px
- 每条微博独占一行（flex row），左右栏同行排列
- 有划线评论时右栏显示评论面板，无评论时右栏空白
- 页面正常滚动，面板随微博一起滚动

### HTML 结构

```html
<div class="tweet-row" data-tweet-id="xxx">
  <div class="tweet-card">
    <!-- 微博卡片内容（含可选中高亮的文本） -->
  </div>
  <div class="annotation-panel" data-tweet-id="xxx">
    <!-- 评论面板（有评论时显示，无评论时为空） -->
  </div>
</div>
```

### 交互流程

**新建评论：**
1. 用户在微博卡片的文本区域选中文字（mouseup 事件）
2. 通过 `window.getSelection()` 获取选区
3. 计算选中文字在所属 field 元素中的 start_offset / end_offset
4. 校验 selected_text 与实际选中文本一致
5. 右侧同行 `.annotation-panel` 出现输入框（显示选中文字 + textarea + 保存/取消按钮）
6. 用户输入评论，点击保存 → POST API
7. 保存成功后：正文对应文字包裹 `<mark>` 高亮，右侧输入框原地变为评论卡片

**编辑评论：**
1. 点击评论卡片上的「编辑」
2. 评论内容原地切换为 textarea
3. 修改后点击保存 → PUT API
4. 保存成功后恢复为展示态

**删除评论：**
1. 点击评论卡片上的「删除」
2. 确认后 → DELETE API
3. 移除正文中的 `<mark>` 高亮
4. 移除右侧评论卡片
5. 如果该微博无其他评论，右栏变为空白

**点击高亮定位：**
1. 点击正文中的 `<mark>` 高亮文字
2. 右侧对应的评论卡片高亮闪烁

### 高亮还原

微博卡片渲染时：
1. 从 API 加载该微博的 annotations 列表
2. 遍历每条 annotation
3. 根据 `field` 找到对应 DOM 元素
4. 取该元素的 `textContent`，截取 `[start_offset, end_offset)` 范围
5. 与 `selected_text` 校验：
   - 匹配 → 用 `<mark class="annotation-highlight" data-annotation-id="xxx">` 包裹
   - 不匹配 → 跳过（内容可能已变化）
6. 高亮文字可点击，点击后右侧对应评论卡片高亮

### offset 计算逻辑

选中文字时计算偏移量：
1. 获取 `window.getSelection().getRangeAt(0)`
2. 确定选区起始节点所属的 `field` 元素（通过 `data-field` 属性或 DOM 位置判断）
3. 创建一个 Range 从 field 元素开头到选区起点，取 `toString().length` 作为 `start_offset`
4. 同理计算 `end_offset`

### CSS 要点

```css
#app { max-width: 1000px; }
.tweet-row { display: flex; gap: 16px; align-items: flex-start; }
.tweet-card { flex: 1; max-width: 680px; }
.annotation-panel { width: 260px; flex-shrink: 0; }

.annotation-highlight {
  background: #fff3cd;
  border-radius: 2px;
  cursor: pointer;
  padding: 1px 2px;
}
.annotation-highlight:hover {
  background: #ffe69c;
}

.annotation-card {
  background: #fff;
  border: 1px solid #e0e0e0;
  border-radius: 8px;
  box-shadow: 0 2px 8px rgba(0,0,0,0.06);
}
.annotation-card.flash {
  animation: flash 0.6s ease;
}
@keyframes flash {
  0%, 100% { background: #fff; }
  50% { background: #e8f0fe; }
}
```

## 文件改动

| 文件 | 改动 |
|------|------|
| `weibospider/db.py` | 新增 annotations 表 + CRUD 方法 |
| `weibospider/app.py` | 新增 4 个 annotation API 端点 |
| `weibospider/static/index.html` | 布局改双栏 + 划线交互逻辑 + 高亮还原 |

## 测试

- `tests/test_db.py`：annotation 表 CRUD 测试
- `tests/test_app.py`：annotation API 端点测试

## 边界情况处理

1. **选区跨元素**：不处理，只支持单个 field 元素内的连续文本选择
2. **选区为空**：不弹出输入框
3. **offset 校验失败**：跳过该高亮，不报错
4. **重复划线同一段文字**：允许，每条评论独立
5. **微博被删除**：annotation 保留（外键不级联删除），但前端不展示
