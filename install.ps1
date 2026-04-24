# gosymdb installer (Windows).
# Installs the gosymdb binary and — if Claude Code is available —
# generates the cli-bridge spec so it registers as a first-class MCP tool.
#
#   iwr -useb https://raw.githubusercontent.com/walkindude/gosymdb/master/install.ps1 | iex
#
# Env vars:
#   $env:GOSYMDB_VERSION   Version to install. Default: latest.
#   $env:GOSYMDB_PREFIX    Install prefix. Default: $env:LOCALAPPDATA\gosymdb
#   $env:GOSYMDB_SKIP_CLI_BRIDGE = 1   Skip cli-bridge spec generation.

$ErrorActionPreference = 'Stop'

$Repo = 'walkindude/gosymdb'
$CliBridgeRepo = 'walkindude/cli-bridge'
$Version = if ($env:GOSYMDB_VERSION) { $env:GOSYMDB_VERSION } else { 'latest' }
$Prefix = if ($env:GOSYMDB_PREFIX) { $env:GOSYMDB_PREFIX } else { Join-Path $env:LOCALAPPDATA 'gosymdb' }
$BinDir = Join-Path $Prefix 'bin'

function Say([string]$msg) { Write-Host $msg }
function Die([string]$msg) { Write-Host "error: $msg" -ForegroundColor Red; exit 1 }

# Detect architecture
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { Die "unsupported arch: $env:PROCESSOR_ARCHITECTURE" }
}
$os = 'windows'

# Resolve version
if ($Version -eq 'latest') {
    Say 'Resolving latest gosymdb release...'
    $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
    $Version = $rel.tag_name
    if (-not $Version) { Die "could not resolve latest release" }
}

# Download
$assetName = "gosymdb_$($Version.TrimStart('v'))_${os}_${arch}.zip"
$downloadUrl = "https://github.com/$Repo/releases/download/$Version/$assetName"
$tmpDir = New-Item -ItemType Directory -Path (Join-Path $env:TEMP "gosymdb-install-$(Get-Random)")
try {
    $zipPath = Join-Path $tmpDir $assetName
    Say "Downloading $downloadUrl"
    Invoke-WebRequest -Uri $downloadUrl -OutFile $zipPath -UseBasicParsing

    # Checksum verify
    try {
        $checksumUrl = "https://github.com/$Repo/releases/download/$Version/checksums.txt"
        $checksumsPath = Join-Path $tmpDir 'checksums.txt'
        Invoke-WebRequest -Uri $checksumUrl -OutFile $checksumsPath -UseBasicParsing
        $expected = (Select-String -Path $checksumsPath -Pattern "\s$([regex]::Escape($assetName))$" | Select-Object -First 1).Line.Split(' ')[0]
        if ($expected) {
            $actual = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash.ToLower()
            if ($actual -ne $expected.ToLower()) { Die "checksum mismatch: expected $expected, got $actual" }
            Say 'Checksum OK'
        }
    } catch {
        Say 'warn: checksum verification skipped'
    }

    Expand-Archive -Path $zipPath -DestinationPath $tmpDir -Force
    New-Item -ItemType Directory -Path $BinDir -Force | Out-Null
    Copy-Item (Join-Path $tmpDir 'gosymdb.exe') -Destination (Join-Path $BinDir 'gosymdb.exe') -Force
    Say "Installed gosymdb.exe to $BinDir"
} finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}

# PATH hint
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$BinDir*") {
    Say ''
    Say "NOTE: $BinDir is not on your User PATH. To add it permanently:"
    Say "  [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';$BinDir', 'User')"
}

# cli-bridge spec
if ($env:GOSYMDB_SKIP_CLI_BRIDGE -ne '1') {
    $claude = Get-Command claude -ErrorAction SilentlyContinue
    if (-not $claude) {
        Say ''
        Say 'Claude Code CLI not found. To enable gosymdb as an MCP tool later:'
        Say "  1. Install Claude Code: https://claude.com/claude-code"
        Say "  2. In a session: /plugin marketplace add $CliBridgeRepo"
        Say '  3. Then: /plugin install cli-bridge@cli-bridge'
        Say '  4. Then: /cli-bridge:register gosymdb'
    } else {
        Say ''
        Say 'Generating cli-bridge spec for gosymdb...'
        $specDir = Join-Path ([Environment]::GetFolderPath('ApplicationData')) 'cli-bridge\specs\gosymdb'
        New-Item -ItemType Directory -Path $specDir -Force | Out-Null
        $binVer = (& (Join-Path $BinDir 'gosymdb.exe') version 2>$null).Split(' ')[1]
        if (-not $binVer) { $binVer = 'dev' }
        $specPath = Join-Path $specDir "$binVer.json"
        try {
            & (Join-Path $BinDir 'gosymdb.exe') cli-bridge-manifest | Out-File -FilePath $specPath -Encoding utf8 -NoNewline
            Say "Wrote cli-bridge spec: $specPath"
            Say ''
            Say 'Next steps (in a Claude Code session):'
            Say "  /plugin marketplace add $CliBridgeRepo"
            Say '  /plugin install cli-bridge@cli-bridge'
            Say ''
            Say 'Then restart Claude Code. gosymdb_* tools will appear in the MCP tool list.'
        } catch {
            Say 'warn: gosymdb cli-bridge-manifest unavailable (older build?).'
        }
    }
}

Say ''
Say 'Done. Try: gosymdb --help'
