# GitHub 内网加速规则（ghaccel233）

> 通用提示词：粘贴到 Codex / dsh / Claude / WorkBuddy / 豆包工作 / MiMo 等任意 agent 的
> 系统提示词、AGENTS.md、.rules 或项目规则中。安装脚本会自动替换 {{SERVER}} / {{PRODUCT_ID}} / {{TOKEN}}。

你需要访问 GitHub（clone、raw 文件、release 下载、API）时，遵守以下规则：

## 配置

- 内网加速服务器: {{SERVER}}
- 本项目 productId: {{PRODUCT_ID}}
- 产品 token: {{TOKEN}}（为空则无需鉴权）
- 若项目根目录存在 `.ghaccel.json`，以其中 server/productId/token/mirrors 为准。

## 访问策略（按顺序）

1. 先用 `curl -fsS -m 3 {{SERVER}}/healthz` 探测内网服务器（输出 ok 即可达）。
2. 可达 → 所有 GitHub URL 重写为内网形式：
   - 规则：`{SERVER}/{productId}/{原始URL去掉 https://}`
   - git clone: `{{SERVER}}/{{PRODUCT_ID}}/github.com/{owner}/{repo}.git`
   - raw: `{{SERVER}}/{{PRODUCT_ID}}/raw.githubusercontent.com/{owner}/{repo}/{ref}/{path}`
   - release: `{{SERVER}}/{{PRODUCT_ID}}/github.com/{owner}/{repo}/releases/download/{tag}/{file}`
   - API: `{{SERVER}}/{{PRODUCT_ID}}/api.github.com/{path}`
   - 也支持完整 URL 形式：`{{SERVER}}/{{PRODUCT_ID}}/https://github.com/...`
3. 不可达 → 回退公共镜像，前缀拼接完整原始 URL，按序尝试：
   `https://gh-proxy.com/` → `https://ghproxy.net/` → `https://ghfast.top/`
   例：`https://gh-proxy.com/https://github.com/{owner}/{repo}.git`
4. 全部失败 → 直连 GitHub，并如实报告网络状况。

## 鉴权

- 产品 token 非空时：curl/API 加头 `X-Product-Token: {{TOKEN}}`。
- git 必须用 Basic Auth（git 会吞掉 URL 查询参数）：
  `git clone http://ghaccel:{{TOKEN}}@{服务器host:port}/{{PRODUCT_ID}}/github.com/{owner}/{repo}.git`

## 硬性约束

- git push 一律直连 `https://github.com/...`，不走加速服务器与公共镜像。
- 若本机 git 配置了 http.proxy（本地代理软件），访问内网服务器时用
  `git -c http.proxy= -c https.proxy= ...` 绕过。
- 私有仓库、GitHub PAT、产品 token 绝不发给公共镜像，只走内网服务器或直连。
- 不修改服务器配置，不探测未授权的 productId。