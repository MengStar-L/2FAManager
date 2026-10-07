param([switch]$SkipChecks)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path $PSScriptRoot -Parent
$metadata = Get-Content (Join-Path $projectRoot 'wails.json') -Raw | ConvertFrom-Json
$version = $metadata.info.productVersion
if ($version -notmatch '^\d+\.\d+\.\d+$') { throw 'Release version must be major.minor.patch.' }
$assetName = "Luma-$version-windows-amd64.exe"
$releaseDirectory = Join-Path $projectRoot "output/releases/v$version"
New-Item -ItemType Directory -Force -Path $releaseDirectory | Out-Null

Push-Location (Join-Path $projectRoot 'frontend')
try {
    npm run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' }
} finally { Pop-Location }

Push-Location $projectRoot
try {
    # Vite must finish before Go reads the embedded frontend resources.
    if (!$SkipChecks) {
        go test ./...
        if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }
        go vet ./...
        if ($LASTEXITCODE -ne 0) { throw 'Go validation failed.' }
    }
    wails build -s -o $assetName
    if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed.' }
    Copy-Item -LiteralPath (Join-Path $projectRoot "build/bin/$assetName") -Destination (Join-Path $releaseDirectory $assetName) -Force
    $portableDirectory = Join-Path $releaseDirectory 'portable'
    New-Item -ItemType Directory -Force -Path $portableDirectory | Out-Null
    Copy-Item -LiteralPath (Join-Path $releaseDirectory $assetName) -Destination (Join-Path $portableDirectory 'Luma.exe') -Force
    Copy-Item -LiteralPath (Join-Path $projectRoot 'README.md') -Destination $portableDirectory -Force
    Copy-Item -LiteralPath (Join-Path $projectRoot 'frontend/src/assets/fonts/OFL.txt') -Destination (Join-Path $portableDirectory 'Nunito-LICENSE.txt') -Force
    Copy-Item -LiteralPath (Join-Path $projectRoot 'frontend/src/assets/fonts/ZCOOLKuaiLe-OFL.txt') -Destination (Join-Path $portableDirectory 'ZCOOLKuaiLe-LICENSE.txt') -Force
    $zipName = "Luma-$version-windows-amd64.zip"
    Compress-Archive -Path (Join-Path $portableDirectory '*') -DestinationPath (Join-Path $releaseDirectory $zipName) -Force
    $lines = @($assetName, $zipName) | ForEach-Object {
        $digest = (Get-FileHash -LiteralPath (Join-Path $releaseDirectory $_) -Algorithm SHA256).Hash.ToLowerInvariant()
        "$digest  $_"
    }
    [System.IO.File]::WriteAllText((Join-Path $releaseDirectory 'SHA256SUMS'), ($lines -join "`n") + "`n", [System.Text.UTF8Encoding]::new($false))
    Write-Output "Release assets: $releaseDirectory"
} finally { Pop-Location }
