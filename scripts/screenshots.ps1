# Regenerates the README screenshots from the demo panel (Windows, Edge).
#   powershell -File scripts/screenshots.ps1 [-Out docs] [-Theme dark]
param([string]$Out = "docs", [int]$Width = 1440, [int]$Height = 900)

$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
$exe = Join-Path $env:TEMP "neotunnel-paneldemo.exe"
$edge = "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe"

Push-Location $root
go build -o $exe ./cmd/paneldemo
Pop-Location

if (-not [IO.Path]::IsPathRooted($Out)) { $Out = Join-Path $root $Out }
New-Item -ItemType Directory -Force $Out | Out-Null

$server = Start-Process -FilePath $exe -ArgumentList "-addr", "127.0.0.1:8099" -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 4   # let the sample benchmark finish
try {
    foreach ($page in "dashboard", "tunnels", "servers", "ports", "speed", "commands") {
        $file = Join-Path $Out "panel-$page.png"
        Start-Process -FilePath $edge -Wait -ArgumentList "--headless=new", "--disable-gpu", "--hide-scrollbars",
            "--window-size=$Width,$Height", "--virtual-time-budget=8000", "--screenshot=$file",
            "http://127.0.0.1:8099/demo/#/$page"
        Write-Output "saved $file"
    }
} finally {
    Stop-Process -Id $server.Id
}
