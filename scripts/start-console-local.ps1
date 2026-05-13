$ErrorActionPreference = "Stop"

$Root = Resolve-Path (Join-Path $PSScriptRoot "..")
$Console = Join-Path $Root "frontend\console"

if (-not (Test-Path (Join-Path $Console "node_modules"))) {
  throw "未找到 frontend/console/node_modules，请先运行：cd frontend/console; npm ci"
}

Set-Location $Console
$env:DISABLE_HMR = "false"
npm run dev
