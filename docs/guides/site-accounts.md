# 网站账户与密码初始化

统一入口为 `https://gaomanyi.site`；`www` 跳转到根域名。`/weibo/` 为站主的微博／雪球，Vue 入口是 `/liquor`、`/ledger`、`/tax`、`/options`，`/account` 查看当前账户并退出。生产安全组入站允许 TCP 80/443，5050/5052 只绑定服务器回环；5051 仅容器内部访问。Caddy 自动申请／续签证书，持久化 `site-certificates` 卷。DNS A 记录需配置根域名和 www。

## 第一次设置密码

服务器预留 `gaomanyi`，没有默认密码。不开放自助注册，只有持有服务器管理权限的人能开通账号。在有 `weibo` SSH 别名的 Mac 上，从本仓库运行：

```sh
bash scripts/open-account-setup.sh gaomanyi
```

浏览器将打开一次性设置页，输入并确认至少 12 个字符的密码即可登录。链接 30 分钟有效，只能使用一次；重新生成即作废旧链接。凭据在 URL 片段中，浏览器读取后移除，不发送到代理访问日志。不要将完整链接发到聊天或公开仓库。普通登录直接打开 `/login`。

公网 HTTPS 尚未就绪时，该脚本会打开限本机访问的临时 SSH 加密通道，可先在自己的 Mac 上完成初始化。此时地址是 `127.0.0.1`；不会把密码通过公网 HTTP 传输。

给新成员开通账户：把命令的用户名换成新名字。若需要把一次性链接私下交给成员，可在**自己的终端**运行 `docker compose exec -T platform /app/server setup 用户名`，将输出的路径加在本站 HTTPS 域名后。忘记密码时使用 `bash scripts/open-account-setup.sh 用户名 recover`。重设完成撤销该账号所有旧会话。普通退出只撤销当前会话。

## 归属与维护

- 用户 ID 与用户名分离，`accounts.db` 保存加盐的 PBKDF2-SHA256 密码校验值（60 万次）、会话摘要与激活凭据摘要。生产 Cookie 为 `__Host-`、Secure、HttpOnly、SameSite=Lax，最长七天。
- 站主的稳定 ID 直接映射原 `platform-data` 目录，既有账本、税表、行情、期权收藏不复制、不改业务主键。新账号只访问 `users/<服务端分配的ID>/` 下自己的库。原微博／雪球档案及配置仅站主可用。匿名 REST、账本 MCP、微博旧公网入口已关闭；归档 MCP 另保留其自己的令牌要求。
- 备份原 `data/`、完整 `platform-data`（包括 `accounts.db` 和 `users/`）及证书卷。运行中使用 SQLite backup API，不直接复制活动库文件，不删除卷。回退页面时也必须保留会话校验，不能重开旧匿名端口。
- 本地开发：回环 Go 服务使用开发 Cookie；Vite 把 `/api/platform/auth/` 代理到 Go。`go run ./cmd/server setup gaomanyi` 生成本地设置路径，搭配 `http://127.0.0.1:5173` 使用。Go 必须已启动并创建 `accounts.db`，两次命令使用同一 `BACKEND_DATA_DIR`。不能把回环开发入口公开代理出去。
