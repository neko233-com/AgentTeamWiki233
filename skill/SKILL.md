---
name: ghaccel233
description: 通过公司内网 ghaccel233 加速服务器访问 GitHub（git clone / raw / releases / API）。当任务需要克隆 GitHub 仓库、下载 raw 文件、拉取 release 资产或调用 GitHub API，且直连缓慢/失败时使用；内网服务器不可达时自动回退公共镜像。
---

# ghaccel233 — GitHub 内网加速

公司内网部署了一台 ghaccel233 加速服务器（仓库：neko233-com/AgentTeamWiki233）。
所有 GitHub 访问优先走内网服务器；每个项目有唯一 `productId`，一台服务器服务多个产品。

## 1. 读取配置（按优先级）

1. 项目根目录 `.ghaccel.json`：
   ```json
   {
     "server": "http://192.168.1.100:8233",
     "productId": "my-product",
     "token": "",
     "mirrors": ["https://gh-proxy.com/", "https://ghproxy.net/", "https://ghfast.top/"]
   }
   ```
2. 环境变量：`GHACCEL_SERVER`、`GHACCEL_PRODUCT_ID`、`GHACCEL_TOKEN`
3. 都没有 → 跳过内网加速，直接用第 4 节的公共镜像。

`token` 为产品访问令牌（服务器端 product.token 非空时才需要）。

## 2. 探测服务器

```bash
curl -fsS -m 3 "$SERVER/healthz"   # 期望输出 ok
```

可达 → 用第 3 节内网重写；不可达 → 用第 4 节公共镜像。不要反复重试内网地址。

## 3. 内网 URL 重写规则

基础形式：`{SERVER}/{productId}/{原始GitHub地址去掉https://}`

| 原始地址 | 重写后 |
|---|---|
| `https://github.com/{owner}/{repo}.git` | `{SERVER}/{pid}/github.com/{owner}/{repo}.git` |
| `https://raw.githubusercontent.com/{owner}/{repo}/{ref}/{path}` | `{SERVER}/{pid}/raw.githubusercontent.com/{owner}/{repo}/{ref}/{path}` |
| `https://github.com/{owner}/{repo}/releases/download/{tag}/{file}` | `{SERVER}/{pid}/github.com/{owner}/{repo}/releases/download/{tag}/{file}` |
| `https://github.com/{owner}/{repo}/archive/refs/heads/{branch}.zip` | `{SERVER}/{pid}/github.com/{owner}/{repo}/archive/refs/heads/{branch}.zip` |
| `https://api.github.com/{path}` | `{SERVER}/{pid}/api.github.com/{path}` |

也支持完整 URL 形式：`{SERVER}/{pid}/https://github.com/...`

### 产品 token 的携带方式（重要）

- curl / wget / API：加请求头 `X-Product-Token: {token}`，或查询参数 `?token={token}`。
- **git 必须用 Basic Auth**（git 会把 `?query` 拼进路径导致查询参数失效）：
  ```bash
  git clone http://ghaccel:{token}@{SERVER_HOST}:{PORT}/{pid}/github.com/{owner}/{repo}.git
  ```
  无 token 的产品直接 `git clone {SERVER}/{pid}/github.com/...`。

### git 注意事项

- 若全局 git 配置了 `http.proxy`（如本地 Clash），访问内网服务器时必须绕过：
  `git -c http.proxy= -c https.proxy= clone ...`，或给该 URL 单独设置空代理。
- **push 一律直连 GitHub**，不走加速服务器：
  ```bash
  git remote set-url --push origin https://github.com/{owner}/{repo}.git
  ```
- 已有仓库想整体走加速（仅拉取）：
  ```bash
  git config url."{SERVER}/{pid}/github.com/".insteadOf "https://github.com/"
  git config url."https://github.com/".pushInsteadOf "{SERVER}/{pid}/github.com/"
  ```

## 4. 公共镜像回退（内网不可达时）

按顺序尝试 `mirrors` 列表（默认 gh-proxy.com → ghproxy.net → ghfast.top），
形式为 `{mirror}{完整原始URL}`：

```bash
curl -fLO "https://gh-proxy.com/https://github.com/{owner}/{repo}/releases/download/{tag}/{file}"
git clone "https://gh-proxy.com/https://github.com/{owner}/{repo}.git"
```

规则：
- 公共镜像只用于**公开资源**。绝不把私有仓库凭据、GitHub PAT、产品 token 发给公共镜像。
- 私有仓库 / 需要认证的 API → 直连 GitHub（可配合本机已有代理）。
- 某个镜像失败换下一个；全部失败则直连并如实报告网络问题。

## 5. 安全红线

- 内网服务器地址、productId、token 只用于内网请求，不写入公开日志/提交。
- 不修改服务器配置；不尝试未授权的 productId。
- 下载的二进制/脚本执行前按项目惯例校验（checksum / 来源）。