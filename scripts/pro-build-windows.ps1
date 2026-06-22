$ErrorActionPreference = "Stop"

$RootDir = Resolve-Path (Join-Path $PSScriptRoot "..")
$PublicKeyFile = if ($env:CCNEXUS_LICENSE_PUBLIC_KEY_FILE) { $env:CCNEXUS_LICENSE_PUBLIC_KEY_FILE } else { Join-Path $env:USERPROFILE ".ccnexus-license\public_key.txt" }
$OutDir = Join-Path $RootDir "dist\pro"

function Invoke-Wails {
    param(
        [Parameter(ValueFromRemainingArguments = $true)]
        [string[]]$WailsArgs
    )

    if (Get-Command wails -ErrorAction SilentlyContinue) {
        & wails @WailsArgs
    } else {
        throw "Wails command not found"
    }

    if ($LASTEXITCODE -ne 0) {
        throw "Wails build failed"
    }
}

function Invoke-WailsOrGoBuild {
    param(
        [string]$PackageDir,
        [string]$OutputName,
        [string]$LdFlags
    )

    Push-Location $PackageDir
    try {
        Invoke-Wails build -clean -ldflags $LdFlags
    } catch {
        if (!(Get-Command go -ErrorAction SilentlyContinue)) {
            Add-Type -AssemblyName PresentationFramework
            [System.Windows.MessageBox]::Show("未找到 Go。请先在打包设备上安装 Go 构建环境，或换到已配置好的打包设备。", "ccNexus Pro", "OK", "Error") | Out-Null
            exit 1
        }
        $BinDir = Join-Path $PackageDir "build\bin"
        New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
        $OutputPath = Join-Path $BinDir $OutputName
        & go build -buildvcs=false -tags "desktop,wv2runtime.download,production" -ldflags $LdFlags -o $OutputPath .
        if ($LASTEXITCODE -ne 0) {
            throw "Go fallback build failed"
        }
    } finally {
        Pop-Location
    }
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

if (![string]::IsNullOrWhiteSpace($env:CCNEXUS_LICENSE_PUBLIC_KEY)) {
    $PublicKey = ($env:CCNEXUS_LICENSE_PUBLIC_KEY -replace "\s", "")
} elseif (!(Test-Path $PublicKeyFile)) {
    Add-Type -AssemblyName PresentationFramework
    Invoke-Item $OutDir
    [System.Windows.MessageBox]::Show("未找到在线授权公钥。请先启动 cmd/license-server 生成公钥，或设置 CCNEXUS_LICENSE_PUBLIC_KEY/CCNEXUS_LICENSE_PUBLIC_KEY_FILE 后再次打包。", "ccNexus Pro", "OK", "Information") | Out-Null
    exit 0
} else {
    $PublicKey = ((Get-Content $PublicKeyFile -Raw) -replace "\s", "")
}

if ([string]::IsNullOrWhiteSpace($PublicKey)) {
    Add-Type -AssemblyName PresentationFramework
    Invoke-Item $OutDir
    [System.Windows.MessageBox]::Show("在线授权公钥为空。请检查 CCNEXUS_LICENSE_PUBLIC_KEY 或公钥文件后再次打包。", "ccNexus Pro", "OK", "Warning") | Out-Null
    exit 0
}

Push-Location (Join-Path $RootDir "cmd\desktop\frontend")
npm install
npm run build
Pop-Location

Invoke-WailsOrGoBuild -PackageDir (Join-Path $RootDir "cmd\desktop") -OutputName "ccNexus.exe" -LdFlags "-w -s -X github.com/lich0821/ccNexus/internal/onlinelicense.AppPublicKey=$PublicKey"

$CustomerExe = Join-Path $RootDir "cmd\desktop\build\bin\ccNexus.exe"
if (!(Test-Path $CustomerExe)) {
    Add-Type -AssemblyName PresentationFramework
    [System.Windows.MessageBox]::Show("未找到 ccNexus.exe，构建失败。", "ccNexus Pro", "OK", "Error") | Out-Null
    exit 1
}

$CustomerZip = Join-Path $OutDir "ccNexus-Pro-windows.zip"
if (Test-Path $CustomerZip) { Remove-Item $CustomerZip -Force }
Compress-Archive -Path $CustomerExe -DestinationPath $CustomerZip
Invoke-Item $OutDir
