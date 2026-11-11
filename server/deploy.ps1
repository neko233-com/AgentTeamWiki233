# ghaccel233 一键部署（Windows PowerShell 5.1+ / PowerShell 7）
# 用法:
#   .\deploy.ps1                                    # 默认端口 8233，productId=default，装到 ~\ghaccel233
#   .\deploy.ps1 -Port 9000 -ProductId team-a -Service
param(
    [int]$Port = 8233,
    [string]$ProductId = "default",
    [string]$InstallDir = "$HOME\ghaccel233",
    [string]$Version = "latest",
    [switch]$Service
)

$ErrorActionPreference = "Stop"
$Repo = "neko233-com/AgentTeamWiki233"
$BinName = "ghaccel233"
# 公共 GitHub 加速镜像（直连失败时按顺序尝试）
$Mirrors = @("https://gh-proxy.com/", "https://ghproxy.net/", "https://ghfast.top/")
if ($env:MIRRORS) { $Mirrors = $env:MIRRORS -split '\s+' }

function Log($msg) { Write-Host "[deploy] $msg" -ForegroundColor Green }
function Die($msg) { Write-Host "[deploy] $msg" -ForegroundColor Red; exit 1 }

$arch = if ([Environment]::Is64BitOperatingSystem) {
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
} else { Die "仅支持 64 位系统" }
$asset = "$BinName-windows-$arch.exe"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$InstallDir = (Resolve-Path $InstallDir).Path
$binPath = Join-Path $InstallDir "$BinName.exe"

function Try-Download([string]$url, [string]$out) {
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
        Invoke-WebRequest -Uri $url -OutFile $out -UseBasicParsing -TimeoutSec 60
        return $true
    } catch { return $false }
}

$built = $false
$goCmd = Get-Command go -ErrorAction SilentlyContinue
if (-not $goCmd) {
    foreach ($p in @("C:\tools\go\bin\go.exe", "C:\Program Files\Go\bin\go.exe", "C:\Go\bin\go.exe", "$HOME\go\bin\go.exe", "$HOME\scoop\apps\go\current\bin\go.exe")) {
        if (Test-Path $p) { $goCmd = @{ Source = $p }; break }
    }
}
if ((Test-Path (Join-Path $PSScriptRoot "main.go")) -and $goCmd) {
    Log "检测到源码与 Go，直接编译…"
    Push-Location $PSScriptRoot
    try { & $goCmd.Source build -trimpath -ldflags "-s -w" -o $binPath . ; if ($LASTEXITCODE -ne 0) { Die "go build 失败" } }
    finally { Pop-Location }
    $built = $true
} else {
    $base = "https://github.com/$Repo/releases"
    $path = if ($Version -eq "latest") { "latest/download/$asset" } else { "download/$Version/$asset" }
    $url = "$base/$path"
    Log "尝试直连下载: $url"
    if (Try-Download $url $binPath) { $built = $true }
    else {
        foreach ($m in $Mirrors) {
            Log "直连失败，尝试镜像: $m"
            if (Try-Download "$m$url" $binPath) { $built = $true; break }
        }
    }
}
if (-not $built) { Die "下载失败且本机无 Go 源码环境。请安装 Go 后在仓库 server\ 目录重试，或手动下载 release: https://github.com/$Repo/releases" }

$configPath = Join-Path $InstallDir "config.json"
if (-not (Test-Path $configPath)) {
    $cacheDir = (Join-Path $InstallDir "cache") -replace '\\', '\\'
    $configJson = @"
{
  "listen": ":$Port",
  "cacheDir": "$cacheDir",
  "cacheMaxMB": 2048,
  "adminToken": "",
  "products": [
    {
      "productId": "$ProductId",
      "name": "$ProductId",
      "token": "",
      "githubToken": "",
      "allowRepos": ["*"]
    }
  ]
}
"@
    [System.IO.File]::WriteAllText($configPath, $configJson, (New-Object System.Text.UTF8Encoding $false))
    Log "已生成配置: $configPath"
} else {
    Log "沿用已有配置: $configPath"
}

# 停掉旧进程
Get-Process -Name $BinName -ErrorAction SilentlyContinue | Stop-Process -Force

if ($Service) {
    $taskName = "ghaccel233"
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue
    $action = New-ScheduledTaskAction -Execute $binPath -Argument "-config `"$configPath`"" -WorkingDirectory $InstallDir
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit (New-TimeSpan -Seconds 0)
    $principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -RunLevel Highest
    Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Settings $settings -Principal $principal | Out-Null
    Start-ScheduledTask -TaskName $taskName
    Log "计划任务 ghaccel233 已注册（开机自启，SYSTEM 运行）"
} else {
    Start-Process -FilePath $binPath -ArgumentList "-config `"$configPath`"" -WorkingDirectory $InstallDir -WindowStyle Hidden
    Log "后台启动完成（无窗口）。日志可用 -Service 模式或自行重定向查看"
}

Start-Sleep -Seconds 2
try {
    $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/healthz" -UseBasicParsing -TimeoutSec 5
    if ($r.Content.Trim() -eq "ok") { Log "健康检查通过 ✔" } else { Die "健康检查返回异常: $($r.Content)" }
} catch { Die "健康检查失败: $_" }

Write-Host @"

部署完成！
  服务地址:   http://<本机IP>:$Port
  productId:  $ProductId
  配置文件:   $configPath

使用示例（把 GitHub 地址拼到 /$ProductId/ 后面）:
  git clone http://<本机IP>:$Port/$ProductId/github.com/owner/repo.git
  curl -O http://<本机IP>:$Port/$ProductId/raw.githubusercontent.com/owner/repo/main/README.md

给 AI agent 接入: 参考仓库 skill\ 目录（install.ps1 一键安装提示词）。
"@