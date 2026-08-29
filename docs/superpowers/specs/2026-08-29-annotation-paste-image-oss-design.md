# 划线评论换行显示 + 粘贴图片上传 OSS - 设计文档

## 概述

修复划线评论（annotation）两个体验问题：

1. **换行不显示**：划线评论输入框里输入换行后，展示时没有真正换行。
2. **无粘贴图片能力**：不支持从剪贴板粘贴图片上传。

图片统一上传到**阿里云 OSS**（非本地磁盘）。

## 现状分析（根因）

- **换行问题根因**：`renderAnnotationPanel`（`weibospider/static/index.html:1626`）用 `esc(ann.comment)` 渲染评论。`esc()` 只做 HTML 转义，不把 `\n` 转为换行；HTML 渲染时 `\n` 被折叠成空格。DB 中 `comment` 本身带 `\n`（textarea 保存时保留），是展示端丢失。
- **无粘贴图片**：划线评论输入框（`showAnnotationInput` 中的 `<textarea>`）没有任何 `paste` 事件处理，也无上传端点。

## 需求确认

- 粘贴图片存储/展示方案：**markdown 内嵌 comment**（`![图片](url)` 内嵌在评论文本，不改 DB 表结构，编辑时可见原文）
- 评论图片展示：**评论下方固定区域**（图片统一排列在评论文字下方，缩略图，点击开灯箱）
- 图片存储：**阿里云 OSS**，用 `oss2` SDK，凭据在设置页 UI 配置 + 存 `config` 表

## 改动一：划线评论换行显示（bug 修复）

纯前端 CSS 修复，不改存储、不破坏编辑回填。

- 给 `.annotation-comment` 加 `white-space: pre-wrap;`
- 给 `.annotation-selected-text` 加 `white-space: pre-wrap;`（跨字段选中文本带 `\n`，同样需要）

**为什么不用 `<br>`**：若把 `\n` 转 `<br>`，`editAnnotation` 用 `commentDiv.textContent` 回填 textarea 时 `<br>` 不产生 textContent，编辑时换行会丢失。`pre-wrap` 让 `\n` 原样换行显示，且 `textContent` 保留原文。

## 改动二：粘贴图片上传阿里云 OSS

### 后端（`weibospider/app.py`）

**OSS 配置（存 config 表）**：

| key | 说明 |
|-----|------|
| `oss_access_key_id` | AK ID |
| `oss_access_key_secret` | AK Secret |
| `oss_bucket` | Bucket 名 |
| `oss_endpoint` | Endpoint，如 `oss-cn-hangzhou.aliyuncs.com` |
| `oss_url_prefix` | 可选自定义域名前缀（如 `https://img.example.com/`），为空则默认 `https://<bucket>.<endpoint>/` |

- `api_config` GET 返回 OSS 配置，secret 打码回显（与 `xq_cookie` 的 `xq_cookie_masked` 模式一致）
- `api_config` POST 增加 OSS 字段解析（5 个 key）
- 新增辅助函数 `_oss_config()` 读取配置、`_mask()` 打码

**上传端点 `POST /api/upload`**：

1. 校验请求带文件（`request.files`）
2. 校验 `Content-Type` 以 `image/` 开头，否则 400
3. 校验大小 ≤ 8MB，否则 400
4. 读取 OSS 配置；缺任一必填（AK/Secret/Bucket/Endpoint）→ 400「请先在设置中配置 OSS」
5. 生成对象 key：`annotations/<uuid>.<ext>`（ext 从 Content-Type 映射：jpeg/png/gif/webp）
6. `oss2.Auth` + `oss2.Bucket` → `put_object(key, data)`，`content_type` 设置为图片 MIME
7. 成功返回 `{"url": "<prefix>annotations/<uuid>.<ext>"}`
8. 失败返回 500 错误 JSON，并记 `insert_log('annotation', 'upload_error', ...)`

**依赖**：`requirements.txt` 增加 `oss2`（Docker 镜像自动安装）

### 前端（`weibospider/static/index.html`）

**设置页「阿里云 OSS」区块**：新增 config-section，含 AK ID / AK Secret / Bucket / Endpoint / URL 前缀 五个输入框 + 保存按钮。`loadConfig()` 回填（secret 回显 `***` 打码值，空白则留空），`saveOssConfig()` POST 保存。

**输入框 paste 上传**：

- `showAnnotationInput` 的 textarea 绑定 `paste` 事件
- 从 `e.clipboardData.items` 找 `type` 以 `image/` 开头的 item
- `item.getAsFile()` → `FormData('file', blob)` → POST `/api/upload`
- 成功后在光标处插入 `![图片](url)` markdown；失败 toast 提示
- 插入后保留 focus

**markdown 图片解析渲染**：

- 新增 `parseMarkdownImages(comment)`：正则 `/!\[([^\]]*)\]\(([^)]+)\)/g` 把 comment 拆成 `{ text, images[] }`，`text` 为去掉图片语法的纯文本，`images` 为图片 URL 数组
- `renderAnnotationPanel` 中：
  - 评论文字用 `esc(parsed.text)` + pre-wrap 显示
  - 图片在评论文字下方 `.annotation-comment-images` 区域统一渲染为 `<img class="annotation-comment-img">`，`src` 为 OSS URL（OSS 公网可读，无需代理），点击 `openLightbox(this.src)`
  - 无图片时该区域不渲染

**编辑时**：`editAnnotation` 的 textarea 仍回填原始 comment（含 `![图片](url)` markdown），用户可继续编辑/删除图片引用。

### 测试

| 文件 | 用例 |
|------|------|
| `tests/test_frontend.py` | 断言 `pre-wrap` CSS、`paste` 处理、`parseMarkdownImages` 函数、OSS 设置区块存在 |
| `tests/test_app.py` | `/api/upload`：非图片文件 400、超大文件 400、未配置 OSS 400 |
| `tests/test_db.py` | OSS 配置 set/get 存取 |

## 边界情况

1. 剪贴板无图片（复制文字）→ 正常粘贴文字，不触发上传
2. 多张图片一次复制 → 逐张上传，全部插入
3. 上传失败 → toast 提示，不插入 markdown
4. 未配置 OSS 就粘贴图片 → 400 错误 toast，提示先配置
5. OSS URL 前缀为空 → 用默认 `https://<bucket>.<endpoint>/`
6. 评论里已有历史 markdown 图片 → 解析渲染，编辑时保留原文

## 安全

- AK Secret 存 config 表，GET 返回时打码（`****` + 后 4 位），与现有 `xq_cookie_masked` 一致
- 上传仅接受 `image/*`，限制 8MB
- 对象 key 用 UUID 随机，避免覆盖/猜测
