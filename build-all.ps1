<#
  GatewayHub 一键式自动化编译
  ===========================
  自动完成：前端构建(Vite) -> 交叉编译全部平台二进制(内嵌前端+IP库) -> UPX 压缩(可选) -> 汇总

  用法（在仓库根目录 PowerShell 中）:
    .\build-all.ps1                      # 全流程（前端 + 全平台）
    .\build-all.ps1 -Platforms windows    # 仅编译 Windows（仍会先构建前端）
    .\build-all.ps1 -SkipFrontend         # 跳过前端构建（用现有 web/dist）
    .\build-all.ps1 -Platforms windows,linux -SkipFrontend

  产物输出: .\release\
  要求: Go 1.21+、Node 18+（npm 在 PATH 中）、data/ip2region.xdb（已入库）
#>
param(
    [string[]]$Platforms = @("windows", "linux", "darwin"),
    [switch]$SkipFrontend
)

$ErrorActionPreference = "Continue"
$root = $PSScriptRoot
$webDir = "$root\web"
$distDir = "$webDir\dist"
$outDir = "$root\release"
$ip2DB = "$root\data\ip2region.xdb"

function Write-Step($msg) { Write-Host "`n============================================" -ForegroundColor Cyan; Write-Host "  $msg" -ForegroundColor Cyan; Write-Host "============================================" -ForegroundColor Cyan }
function Write-OK($msg)   { Write-Host "  [OK] $msg" -ForegroundColor Green }
function Write-Fail($msg) { Write-Host "  [FAIL] $msg" -ForegroundColor Red }

# ---------------- 0. 前置检查 ----------------
Write-Step "GatewayHub 一键构建 - 前置检查"

$goCmd = Get-Command go -ErrorAction SilentlyContinue
if (-not $goCmd) { Write-Fail "未找到 go，请安装 Go 1.21+ 并加入 PATH"; exit 1 }
Write-OK "go: $(go version)"

$npmCmd = Get-Command npm -ErrorAction SilentlyContinue
if (-not $npmCmd) { Write-Fail "未找到 npm，请安装 Node 18+ 并加入 PATH"; exit 1 }
Write-OK "npm: $((& npm --version) 2>&1 | Select-Object -First 1)"

if (-not (Test-Path $ip2DB)) { Write-Fail "缺少 $ip2DB（IP 库已入库，请勿删除）"; exit 1 }
Write-OK "IP 库: $(Split-Path $ip2DB -Leaf) ($([math]::Round((Get-Item $ip2DB).Length/1MB,1)) MB)"

# ---------------- 1. 前端构建 ----------------
if (-not $SkipFrontend) {
    Write-Step "1/3 前端构建 (Vite)"
    Push-Location $webDir
    try {
        if (-not (Test-Path "node_modules")) {
            Write-Host "  npm install ..."
            npm install --no-audit --no-fund 2>&1 | Out-Null
            if ($LASTEXITCODE -ne 0) { Write-Fail "npm install 失败"; Pop-Location; exit 1 }
        }
        Write-Host "  npm run build ..."
        npm run build 2>&1 | Out-Null
        if ($LASTEXITCODE -ne 0) { Write-Fail "前端构建失败"; Pop-Location; exit 1 }
    } finally { Pop-Location }
    Write-OK "前端构建完成"
} else {
    Write-Step "1/3 前端构建 (跳过，使用现有 web/dist)"
}
if (-not (Test-Path "$distDir\index.html")) { Write-Fail "web/dist/index.html 不存在（构建失败或目录缺失）"; exit 1 }
Write-OK "dist: index.html + assets"

# ---------------- 2. 清理输出目录 ----------------
Write-Step "2/3 准备输出目录"
if (Test-Path $outDir) {
    # 保留旧包备份，避免构建中断丢失
    $oldDir = "$outDir.old"
    if (Test-Path $oldDir) { Remove-Item -Recurse -Force $oldDir -ErrorAction SilentlyContinue }
    Rename-Item $outDir $oldDir -ErrorAction SilentlyContinue
    Write-Host "  旧产物已备份到 $oldDir"
}
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

# ---------------- 3. 交叉编译 ----------------
# 注：数据库驱动 glebarez/sqlite（modernc.org/sqlite 纯 Go 移植）不支持的架构
# （windows/386、windows/arm、linux/mips64）已排除；其余主流平台均可 CGO_ENABLED=0 交叉编译。
$targets = @(
    @{OS="windows"; Arch="amd64";  Name="gatewayhub-win-x64.exe";     Desc="Windows 64-bit"},
    @{OS="windows"; Arch="arm64";  Name="gatewayhub-win-arm64.exe";   Desc="Windows ARM64"},
    @{OS="linux";   Arch="386";    Name="gatewayhub-linux-x32";       Desc="Linux 32-bit"},
    @{OS="linux";   Arch="amd64";  Name="gatewayhub-linux-x64";       Desc="Linux 64-bit"},
    @{OS="linux";   Arch="arm";    Name="gatewayhub-linux-arm32";     Desc="Linux ARM32(v7)";   Goarm="7"},
    @{OS="linux";   Arch="arm";    Name="gatewayhub-linux-armv6";     Desc="Linux ARM32(v6)";   Goarm="6"},
    @{OS="linux";   Arch="arm64";  Name="gatewayhub-linux-arm64";     Desc="Linux ARM64"},
    @{OS="linux";   Arch="riscv64";Name="gatewayhub-linux-riscv64";   Desc="Linux RISC-V64"},
    @{OS="darwin";  Arch="amd64";  Name="gatewayhub-mac-x64";         Desc="macOS Intel"},
    @{OS="darwin";  Arch="arm64";  Name="gatewayhub-mac-arm64";       Desc="macOS Apple Silicon"}
)

# 过滤平台
$targets = @($targets | Where-Object { $_.OS -in $Platforms })
if ($targets.Count -eq 0) {
    Write-Fail "平台参数无效: $($Platforms -join ',')（可选: windows / linux / darwin）"
    exit 1
}
Write-Step "3/3 交叉编译 ($($targets.Count) 个目标)"

# UPX 压缩器（可选，存在则启用；参考 nvs 布局）
$upxExe = "$root\..\upx\upx-5.2.0-win64\upx.exe"
$upxAvailable = Test-Path $upxExe
if ($upxAvailable) { Write-OK "UPX: $upxExe" } else { Write-Host "  UPX 未找到，跳过压缩（体积会偏大）" -ForegroundColor Yellow }

$ok = 0; $fail = 0; $idx = 0
foreach ($t in $targets) {
    $idx++
    $env:CGO_ENABLED = "0"
    $env:GOOS = $t.OS
    $env:GOARCH = $t.Arch
    $env:GOAMD64 = ""; $env:GO386 = ""
    if ($t.Goarm) { $env:GOARM = $t.Goarm } else { Remove-Item Env:GOARM -ErrorAction SilentlyContinue }

    $outFile = "$outDir\$($t.Name)"
    Write-Host "[$idx/$($targets.Count)] $($t.Desc) " -NoNewline

    Push-Location $root
    $buildErr = (go build -ldflags="-s -w" -o $outFile . 2>&1)
    $rc = $LASTEXITCODE
    Pop-Location

    if ($rc -eq 0) {
        # UPX 压缩（跳过 macOS Mach-O 与 Windows ARM64 PE，格式不支持）
        $skipUpx = ($t.OS -eq "darwin") -or ($t.OS -eq "windows" -and $t.Arch -eq "arm64")
        if ($upxAvailable -and -not $skipUpx) {
            & $upxExe --best --force $outFile 2>&1 | Out-Null
        }
        $sz = [math]::Round((Get-Item $outFile).Length / 1MB, 1)
        Write-Host "OK  (${sz} MB)" -ForegroundColor Green
        $ok++
    } else {
        Write-Host "FAIL" -ForegroundColor Red
        Write-Host "    $buildErr" -ForegroundColor DarkRed
        $fail++
    }
}

# ---------------- 汇总 ----------------
Write-Host ""
Write-Step "构建结果: $ok 成功, $fail 失败"
if ($fail -eq 0) {
    Write-Host "  全部产物位于: $outDir" -ForegroundColor Cyan
    Get-ChildItem $outDir | ForEach-Object {
        Write-Host ("    {0,-32} {1,8:N1} MB" -f $_.Name, ($_.Length / 1MB))
    }
    Write-Host "`n完成！可分发整个 release 目录（每个二进制均为单文件自包含：前端+IP库内嵌）。" -ForegroundColor Green
    if (-not $upxAvailable) {
        Write-Host "提示: 将 upx.exe 放到 ..\upx\upx-5.2.0-win64\ 可启用压缩减小体积。" -ForegroundColor Yellow
    }
    exit 0
} else {
    Write-Host "存在失败目标，请检查上方错误信息。" -ForegroundColor Red
    exit 1
}
