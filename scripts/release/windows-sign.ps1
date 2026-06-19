param(
    [Parameter(Mandatory = $true)]
    [string]$FilePath,

    [string]$CertificatePath = $env:WINDOWS_CERTIFICATE_PFX,
    [string]$CertificatePassword = $env:WINDOWS_CERTIFICATE_PASSWORD
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $FilePath)) {
    throw "File not found: $FilePath"
}

if (-not $CertificatePath -or -not (Test-Path $CertificatePath)) {
    throw "Set WINDOWS_CERTIFICATE_PFX or pass -CertificatePath"
}

if (-not $CertificatePassword) {
    throw "Set WINDOWS_CERTIFICATE_PASSWORD or pass -CertificatePassword"
}

$signtool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin" -Recurse -Filter signtool.exe |
    Where-Object { $_.FullName -match "\\x64\\signtool.exe$" } |
    Select-Object -First 1 -ExpandProperty FullName

if (-not $signtool) {
    throw "signtool.exe not found. Install the Windows SDK."
}

& $signtool sign /f $CertificatePath /p $CertificatePassword /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 $FilePath
& $signtool verify /pa /v $FilePath

Write-Host "Signed and verified: $FilePath"
