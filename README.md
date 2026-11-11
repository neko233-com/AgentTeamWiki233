# AgentTeamWiki233 (ghaccel233)

公司内网 GitHub 加速方案：**一台自部署服务器 + 一份 AI agent 提示词（skill）**，
让内网里的所有产品、所有 coding agent（Codex / dsh / Claude Code / WorkBuddy / 豆包工作 / MiMo …）
都能快速访问 GitHub。

```
┌─────────────────────────── 公司内网 ───────────────────────────┐
│                                                                │
│  产品A 的项目们          产品B 的项目们         产品C …          │
│  productId=product-a    productId=product-b                    │
│  (Codex/Claude/豆包…)   (dsh/WorkBuddy/MiMo…)                  │
│        │                      │                                │
│        └──────────┬───────────┘                                │
│                   ▼                                            │
│        ghaccel233 服务器（一台，多产品共用）                     │
│        :8233  按 productId 路由 / 磁盘缓存 / 白名单              │
└───────────────────┬────────────────────────────────────────────┘
                    ▼  （服务器出网，可配代理）
        github.com / raw.githubusercontent.com / api.github.com …
```

- **内网可达** → agent 把 GitHub URL 重写到 `http://服务器:8233/{productId}/...`，命中缓存秒回。
- **内网不可达**（出差/居家）→ skill 自动回退公共镜像（gh-proxy.com 等），再不行直连。

## 目录结构

```
server/   Go 单二进制加速服务器 + ps1/sh 一键部署 + Docker
skill/    agent 提示词（SKILL.md / PROMPT.md）+ 一键安装脚本
```

## 快速开始

### 1. 部署服务器（任选一台内网机器）

Windows（PowerShell）：

```powershell
git clone https://github.com/neko233-com/AgentTeamWiki233.git
cd AgentTeamWiki233\server
.\deploy.ps1 -Port 8233 -ProductId product-a
# 开机自启（SYSTEM 计划任务）: .\deploy.ps1 -Service
```

Linux / macOS：

```bash
git clone https://github.com/neko233-com/AgentTeamWiki233.git
cd AgentTeamWiki233/server
./deploy.sh --port 8233 --product-id product-a
# root + systemd 开机自启: sudo ./deploy.sh --service
```

脚本优先从 GitHub Release 下载预编译二进制（**直连失败自动尝试公共镜像加速**），
有源码和 Go 时直接编译。也可以用 Docker：

```bash
cd server && mkdir data   # 把 config.json 放进 data/
docker compose up -d
```

### 2. 给 AI agent 接入 skill

```powershell
cd skill
.\install.ps1 -Server http://<服务器IP>:8233 -ProductId product-a
```

```bash
cd skill
./install.sh --server http://<服务器IP>:8233 --product-id product-a
```

安装目标（`-Target` / `--target`）：

| target | 位置 | 适用 |
|---|---|---|
| `claude` | `~/.claude/skills/ghaccel233/SKILL.md` | Claude Code |
| `codex` | `~/.codex/AGENTS.md`（幂等标记块） | Codex CLI |
| `generic` | `~/.ghaccel233/PROMPT.md` | dsh / WorkBuddy / 豆包工作 / MiMo 等，粘贴进各自的系统提示词/项目规则 |

每个项目还可以放一份 `.ghaccel.json`（模板见 `skill/ghaccel.example.json`），
agent 会优先读它，实现**同一台服务器、不同项目不同 productId**：

```json
{ "server": "http://192.168.1.100:8233", "productId": "product-a", "token": "" }
```

### 3. 手动使用（不接 agent 也行）

```bash
# git clone（产品无 token 时）
git clone http://192.168.1.100:8233/product-a/github.com/owner/repo.git

# raw 文件
curl -O http://192.168.1.100:8233/product-a/raw.githubusercontent.com/owner/repo/main/README.md

# release 资产（自动跟随重定向 + 缓存 7 天）
curl -LO http://192.168.1.100:8233/product-a/github.com/owner/repo/releases/download/v1.0/app.zip

# 完整 URL 形式（ghproxy 风格）
curl -O "http://192.168.1.100:8233/product-a/https://github.com/owner/repo/archive/refs/heads/main.zip"
```

## productId 与多产品

一台服务器在 `config.json` 里声明多个产品，每个产品独立配置：

```json
{
  "listen": ":8233",
  "cacheDir": "cache",
  "cacheMaxMB": 2048,
  "adminToken": "",
  "products": [
    {
      "productId": "product-a",
      "name": "产品 A",
      "token": "",
      "githubToken": "",
      "allowRepos": ["neko233-com/*"]
    },
    {
      "productId": "product-b",
      "token": "secret-xxx",
      "githubToken": "ghp_xxx",
      "allowRepos": ["*"]
    }
  ]
}
```

| 字段 | 说明 |
|---|---|
| `productId` | 路由键，URL 第一段；字母/数字/_/-，≤64 字符 |
| `token` | 产品访问令牌（可选）。curl 用 `X-Product-Token` 头或 `?token=`；**git 用 Basic Auth**：`http://ghaccel:<token>@host:port/{pid}/...`（git 会把查询参数拼进路径，不能走 `?token=`） |
| `githubToken` | 上游 GitHub PAT（可选），用于私有仓库/提升 API 限额；客户端自带 Authorization 时优先用客户端的 |
| `allowHosts` | 允许代理的域名白名单，默认 GitHub 全家桶 |
| `allowRepos` | 仓库 glob 白名单，如 `["neko233-com/*"]`，默认 `["*"]` |

## 缓存策略

- 只缓存 GET 200：raw / codeload / release 资产 / archive / objects。
- TTL：release 资产与 commit-SHA 固定的 raw 7 天；分支 raw 10 分钟；archive 1 小时。
- 容量上限 `cacheMaxMB`，超限按最久未访问淘汰。响应头 `X-Cache: HIT/MISS`。
- git 协议（info/refs、upload-pack、receive-pack）全程流式转发，不缓存。
- `GET /admin/stats` 查看各产品请求数/缓存命中/流量（配了 `adminToken` 需带 `X-Admin-Token`）。

## 注意事项

- **push 直连 GitHub**：加速服务器定位是读加速；skill 已要求 agent push 走原始地址。
- 本机 git 若配了 `http.proxy`（Clash 等），访问内网服务器要绕过：`git -c http.proxy= clone ...`。
- 服务器出网受限时，给进程配 `HTTPS_PROXY` 环境变量即可（Go 标准库自动识别）。
- 公共镜像回退只用于公开资源；私有仓库凭据绝不经过公共镜像。
- 首次运行会自动生成默认 `config.json`（productId=default）；二进制不依赖任何运行时。

## 构建与发布

```bash
cd server
go build -trimpath -ldflags "-s -w" -o ghaccel233 .
```

打 tag（如 `v1.0.0`）触发 GitHub Actions，自动构建 linux/windows × amd64/arm64
并发布到 Release，部署脚本即可免 Go 下载。

## License

MIT