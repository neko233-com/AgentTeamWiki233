# ghaccel233 skill 安装（Windows PowerShell 5.1+ / PowerShell 7）
# 用法:
#   .\install.ps1 -Server http://192.168.1.100:8233 -ProductId my-product [-Token xxx] [-Target all]
# Target: claude | codex | generic | all（默认 all）
#   claude  → ~\.claude\skills\ghaccel233\SKILL.md
#   codex   → ~\.codex\AGENTS.md（标记块幂等更新）
#   generic → ~\.ghaccel233\PROMPT.md（dsh / WorkBuddy / 豆包工作 / MiMo 等手动粘贴）
param(
    [Parameter(Mandatory = $true)][string]$Server,
    [Parameter(Mandatory = $true)][string]$ProductId,
    [string]$Token = "",
    [ValidateSet("claude", "codex", "generic", "all")][string]$Target = "all"
)

$ErrorActionPreference = "Stop"
$utf8NoBom = New-Object System.Text.UTF8Encoding $false

function Log($msg) { Write-Host "[skill] $msg" -ForegroundColor Green }

function Render([string]$src, [string]$dst) {
    $text = [System.IO.File]::ReadAllText($src)
    $text = $text.Replace("{{SERVER}}", $Server).Replace("{{PRODUCT_ID}}", $ProductId).Replace("{{TOKEN}}", $Token)
    $dir = Split-Path $dst -Parent
    if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
    [System.IO.File]::WriteAllText($dst, $text, $utf8NoBom)
}

function Install-Claude {
    Render (Join-Path $PSScriptRoot "SKILL.md") "$HOME\.claude\skills\ghaccel233\SKILL.md"
    Log "Claude Code skill → ~\.claude\skills\ghaccel233\SKILL.md"
}

function Install-Codex {
    $dst = "$HOME\.codex\AGENTS.md"
    $rendered = [System.IO.File]::ReadAllText((Join-Path $PSScriptRoot "PROMPT.md"))
    $rendered = $rendered.Replace("{{SERVER}}", $Server).Replace("{{PRODUCT_ID}}", $ProductId).Replace("{{TOKEN}}", $Token)
    $block = "<!-- ghaccel233:start -->`n$rendered`n<!-- ghaccel233:end -->"
    if (-not (Test-Path (Split-Path $dst -Parent))) { New-Item -ItemType Directory -Force -Path (Split-Path $dst -Parent) | Out-Null }
    if (Test-Path $dst) {
        $content = [System.IO.File]::ReadAllText($dst)
        $pattern = '(?s)<!-- ghaccel233:start -->.*?<!-- ghaccel233:end -->'
        if ($content -match 'ghaccel233:start') {
            $content = [regex]::Replace($content, $pattern, [System.Text.RegularExpressions.MatchEvaluator]{ param($m) $block })
        } else {
            $content = $content + "`n" + $block
        }
    } else {
        $content = $block
    }
    [System.IO.File]::WriteAllText($dst, $content, $utf8NoBom)
    Log "Codex 全局规则 → ~\.codex\AGENTS.md"
}

function Install-Generic {
    Render (Join-Path $PSScriptRoot "PROMPT.md") "$HOME\.ghaccel233\PROMPT.md"
    Log "通用提示词 → ~\.ghaccel233\PROMPT.md"
    Log "dsh / WorkBuddy / 豆包工作 / MiMo 等：把该文件内容粘贴进对应 agent 的系统提示词/项目规则即可"
}

switch ($Target) {
    "claude"  { Install-Claude }
    "codex"   { Install-Codex }
    "generic" { Install-Generic }
    "all"     { Install-Claude; Install-Codex; Install-Generic }
}

try {
    $r = Invoke-WebRequest -Uri "$Server/healthz" -UseBasicParsing -TimeoutSec 3
    if ($r.Content.Trim() -eq "ok") { Log "服务器健康检查通过 ✔ ($Server)" }
} catch {
    Log "警告: $Server 当前不可达，agent 将自动回退公共镜像"
}