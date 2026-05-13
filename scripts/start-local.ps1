$ErrorActionPreference = "Stop"

$Root = Resolve-Path (Join-Path $PSScriptRoot "..")
$BackendScript = Join-Path $Root "scripts\start-backend-local.ps1"
$WorkspaceScript = Join-Path $Root "scripts\start-workspace-local.ps1"
$ConsoleScript = Join-Path $Root "scripts\start-console-local.ps1"

Start-Process powershell -ArgumentList @(
  "-NoExit",
  "-ExecutionPolicy",
  "Bypass",
  "-File",
  $BackendScript
)

Start-Process powershell -ArgumentList @(
  "-NoExit",
  "-ExecutionPolicy",
  "Bypass",
  "-File",
  $WorkspaceScript
)

Start-Process powershell -ArgumentList @(
  "-NoExit",
  "-ExecutionPolicy",
  "Bypass",
  "-File",
  $ConsoleScript
)

Write-Host "本地服务启动中："
Write-Host "Django: http://127.0.0.1:8000"
Write-Host "用户端 Vite:   http://127.0.0.1:3000"
Write-Host "管理端 Vite:   http://127.0.0.1:3001"
