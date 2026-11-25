#requires -Version 5.1
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

$source = Join-Path $PSScriptRoot "src\dsxid.js"
$output = Join-Path $PSScriptRoot "dsxid.js"
if (-not (Test-Path -LiteralPath $source)) {
    throw "Missing browser SDK source: $source"
}

$npx = Get-Command "npx.cmd" -ErrorAction SilentlyContinue
if (-not $npx) {
    throw "npx.cmd is missing. Install Node.js first."
}

& $npx.Source --yes javascript-obfuscator@4.1.1 $source `
    --output $output `
    --target browser `
    --compact true `
    --self-defending true `
    --string-array true `
    --string-array-encoding base64 `
    --string-array-threshold 0.75 `
    --string-array-rotate true `
    --string-array-shuffle true `
    --split-strings true `
    --split-strings-chunk-length 6 `
    --control-flow-flattening true `
    --control-flow-flattening-threshold 0.5 `
    --dead-code-injection true `
    --dead-code-injection-threshold 0.2 `
    --identifier-names-generator hexadecimal `
    --seed 20261124
if ($LASTEXITCODE -ne 0) {
    throw "Browser SDK obfuscation failed."
}

$author = "Dami" + [char]0x00E1 + "n S" + [char]0x00FD + "kora"
$banner = "/*! DsxID browser SDK | Copyright (c) 2026 $author | MIT License */`r`n"
$code = [System.IO.File]::ReadAllText($output)
[System.IO.File]::WriteAllText($output, $banner + $code, (New-Object System.Text.UTF8Encoding($false)))

$landingSdk = Join-Path (Split-Path -Parent $PSScriptRoot) "landing\sdk"
New-Item -ItemType Directory -Force -Path $landingSdk | Out-Null
Copy-Item $output (Join-Path $landingSdk "dsxid.js") -Force
Copy-Item (Join-Path $PSScriptRoot "dsxid.css") (Join-Path $landingSdk "dsxid.css") -Force

Write-Host "Wrote $output" -ForegroundColor Green
Write-Host "Synced $landingSdk" -ForegroundColor Green
