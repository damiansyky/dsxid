#requires -Version 5.1
$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location $projectRoot

if (-not (Get-Command wails -ErrorAction SilentlyContinue)) {
    throw "Wails CLI is missing. Install it with: go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.1"
}

wails build -clean -s -skipbindings -trimpath -nopackage
if ($LASTEXITCODE -ne 0) { throw "Build failed." }

Copy-Item "build/bin/DsxID.exe" "DsxID.exe" -Force
New-Item -ItemType Directory -Force -Path "landing/download" | Out-Null
Copy-Item "build/bin/DsxID.exe" "landing/download/DsxID.exe" -Force
Remove-Item "build/bin" -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item "frontend/wailsjs" -Recurse -Force -ErrorAction SilentlyContinue
Write-Host ""
Write-Host "Done: $(Join-Path $projectRoot 'DsxID.exe')" -ForegroundColor Green
Write-Host "Site download: $(Join-Path $projectRoot 'landing\download\DsxID.exe')" -ForegroundColor Green
