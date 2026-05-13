$ErrorActionPreference = "Stop"

$Root = Resolve-Path (Join-Path $PSScriptRoot "..")
$Workspace = Join-Path $Root "frontend\workspace"

if (-not (Test-Path (Join-Path $Workspace "node_modules"))) {
  throw "未找到 frontend/workspace/node_modules，请先运行：cd frontend/workspace; npm ci"
}

Set-Location $Workspace
$env:DISABLE_HMR = "false"
npm run dev
