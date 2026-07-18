# 服务器部署准备

在执行一键部署前，需在服务器上完成以下一次性准备。

## 1. 系统要求

- Ubuntu 20.04 / 22.04 / 24.04（Debian 11+ 亦可）
- 公网 IP，开放 5050 端口（或自定义）和 SSH 端口
- root 或 sudo 权限

## 2. 创建部署用户

```bash
sudo useradd -m -s /bin/bash deploy
sudo usermod -aG docker deploy  # 安装 Docker 后执行
```

## 3. 配置 SSH 免密登录

在开发机上：

```bash
ssh-keygen -t ed25519 -f ~/.ssh/weibospider_deploy  # 若已有 key 可跳过
ssh-copy-id -i ~/.ssh/weibospider_deploy.pub deploy@<服务器IP>
```

测试：

```bash
ssh -i ~/.ssh/weibospider_deploy deploy@<服务器IP> 'echo ok'
```

## 4. 授权目录

```bash
sudo mkdir -p /opt/weibospider
sudo chown deploy:deploy /opt/weibospider
```

## 5. 首次部署

```bash
curl -fsSL https://raw.githubusercontent.com/Banana1995/WeiboSpider/master/install.sh | sudo bash
```

## 6. 配置 GitHub Secrets

在仓库 Settings → Secrets and variables → Actions 添加：

| Secret | 示例 |
|--------|------|
| SSH_HOST | 1.2.3.4 |
| SSH_USER | deploy |
| SSH_KEY | （~/.ssh/weibospider_deploy 的完整私钥内容） |
| SSH_PORT | 22 |
| SMTP_HOST | smtp.qq.com |
| SMTP_PORT | 465 |
| SMTP_USER | you@qq.com |
| SMTP_PASS | （邮箱授权码） |
| MAIL_TO | you@qq.com |

## 7. 验证自动部署

```bash
# 在开发机
git push origin master
# 去 GitHub Actions 页面查看 deploy 任务执行情况
# 邮箱应收到部署成功通知
```

## 8. 日常更新

之后每次 `git push origin master` 即自动部署。也可手动在服务器执行：

```bash
ssh deploy@<服务器IP>
cd /opt/weibospider && ./update.sh
```

## 9. 数据备份

数据在 `/opt/weibospider/data/`，备份：

```bash
cp -r /opt/weibospider/data /backup/weibospider-data-$(date +%F)
```

## 10. 查看日志

```bash
cd /opt/weibospider
docker compose logs -f          # 实时日志
docker compose logs --tail 100  # 最近 100 行
```
