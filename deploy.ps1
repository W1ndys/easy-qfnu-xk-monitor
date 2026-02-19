<#
.SYNOPSIS
    Go 项目自动化部署脚本 (Tar + SSH)

.DESCRIPTION
    1. 自动检测本地 SSH 密钥
    2. 检查并自动创建远程目标目录
    3. 使用 tar + ssh 管道流式上传文件（排除 .git, .env 等敏感文件）
    4. 远程执行重启命令

.EXAMPLE
    .\deploy.ps1 -Server "192.168.1.100" -RemotePath "/app/qfnu-cas"
#>

param(
    # 服务器地址
    [string]$Server = "my.server",

    # SSH 端口
    [int]$Port = 22,

    # 登录用户
    [string]$User = "root",

    # 远程部署路径 (默认: /root/easy-qfnu-xk-monitor)
    [string]$RemotePath = "/root/easy-qfnu-xk-monitor",

    # 部署完成后执行的命令
    [string]$RestartCmd = "echo 'Deploy finished, no restart command specified.'",

    # 本地项目路径
    [string]$LocalPath = ".",

    # SSH 私钥路径 (留空则自动检测)
    [string]$IdentityFile = ""
)

$ErrorActionPreference = "Stop"

# 1. 自动检测 SSH 密钥
if ([string]::IsNullOrEmpty($IdentityFile)) {
    $sshDir = "$env:USERPROFILE\.ssh"
    $possibleKeys = @("id_rsa", "id_ed25519")

    foreach ($keyName in $possibleKeys) {
        $path = Join-Path $sshDir $keyName
        if (Test-Path $path) {
            $IdentityFile = $path
            Write-Host "[-] 自动检测到 SSH 密钥: $IdentityFile" -ForegroundColor Cyan
            break
        }
    }
}

if (-not (Test-Path $IdentityFile)) {
    Write-Error "未找到 SSH 密钥，请通过 -IdentityFile 参数指定。"
}

# 构建基础 SSH 命令前缀
$sshCmdPrefix = @("ssh", "-i", "$IdentityFile", "-p", "$Port", "-o", "StrictHostKeyChecking=no", "$User@$Server")

# 2. 交叉编译 (Windows -> Linux)
Write-Host "[-] 正在编译 Linux (amd64) 二进制文件..." -ForegroundColor Cyan
$ProjectName = "easy-qfnu-xk-monitor"
$TargetOS = "linux"
$TargetArch = "amd64"
$BinaryName = "${ProjectName}-${TargetOS}-${TargetArch}"

# 保存旧的环境变量
$OriginalGOOS = $env:GOOS
$OriginalGOARCH = $env:GOARCH

try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = $TargetOS
    $env:GOARCH = $TargetArch

    go build -ldflags "-s -w" -o $BinaryName .

    if ($LASTEXITCODE -ne 0) {
        Write-Error "编译失败，请检查 Go 环境或代码错误。"
    }
    Write-Host "[-] 编译成功: $BinaryName" -ForegroundColor Green
}
finally {
    # 恢复环境变量
    $env:GOOS = $OriginalGOOS
    $env:GOARCH = $OriginalGOARCH
}

# 3. 检查并修复远程路径 (mkdir -p)
Write-Host "[-] 正在检查/创建远程目录: $RemotePath" -ForegroundColor Cyan
$mkdirCmd = $sshCmdPrefix + "mkdir -p $RemotePath"
& $mkdirCmd[0] $mkdirCmd[1..($mkdirCmd.Length-1)]
if ($LASTEXITCODE -ne 0) {
    Write-Error "无法创建远程目录，请检查连接或权限。"
}

# 4. 使用 Tar + SSH 上传二进制文件
Write-Host "[-] 正在上传二进制文件..." -ForegroundColor Cyan

# 上传逻辑：
# 1. 本地 tar -cf - 将归档输出到 stdout（避免默认写入 tape 设备）
# 2. 通过 SSH 管道传输
# 3. 远程 tar -xf - 解压到目标目录，并赋予可执行权限
$remoteExtractCmd = "tar -xf - -C '$RemotePath' && chmod +x '$RemotePath/$BinaryName'"

Write-Host "Executing: Upload..." -ForegroundColor DarkGray
tar -cf - -- $BinaryName | ssh -i "$IdentityFile" -p $Port -o StrictHostKeyChecking=no "$User@$Server" $remoteExtractCmd

if ($LASTEXITCODE -eq 0) {
    Write-Host "[+] 文件上传成功!" -ForegroundColor Green

    # 上传完成后清理本地构建产物，避免工作目录堆积历史二进制
    if (Test-Path $BinaryName) {
        try {
            Remove-Item -LiteralPath $BinaryName -Force
            Write-Host "[-] 已删除本地构建文件: $BinaryName" -ForegroundColor Green
        } catch {
            Write-Warning "上传成功，但删除本地构建文件失败: $BinaryName，错误: $($_.Exception.Message)"
        }
    }
} else {
    Write-Error "文件上传失败。"
}

# 5. 执行远程重启命令
if (-not [string]::IsNullOrEmpty($RestartCmd)) {
    Write-Host "[-] 正在执行远程命令: $RestartCmd" -ForegroundColor Cyan
    $remoteExec = $sshCmdPrefix + $RestartCmd
    & $remoteExec[0] $remoteExec[1..($remoteExec.Length-1)]

    if ($LASTEXITCODE -eq 0) {
        Write-Host "[+] 远程命令执行成功!" -ForegroundColor Green
    } else {
        Write-Warning "远程命令执行返回了非零状态码。"
    }
}

Write-Host "`n部署流程结束。" -ForegroundColor Cyan
