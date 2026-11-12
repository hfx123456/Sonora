param([Parameter(Mandatory=$true)][string]$LyricifySource, [switch]$FrameworkDependent)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$repositoryRoot = Split-Path -Parent $projectRoot
$dotnetExecutable = Join-Path $env:ProgramFiles 'dotnet/dotnet.exe'
$publishDirectory = Join-Path $PSScriptRoot $(if ($FrameworkDependent) { 'bin/publish' } else { 'bin/publish-self-contained' })
$selfContained = (-not $FrameworkDependent).ToString().ToLowerInvariant()
& $dotnetExecutable publish (Join-Path $PSScriptRoot 'FusionBridge.csproj') -c Release -r win-x64 --self-contained $selfContained "-p:LyricifySource=$LyricifySource" -o $publishDirectory --nologo
if ($LASTEXITCODE -ne 0) { throw 'Lyricify helper build failed' }
Copy-Item -LiteralPath (Join-Path $publishDirectory 'MySpotify.Lyrics.exe') -Destination (Join-Path $repositoryRoot 'internal/fusion/bridge.bin') -Force
