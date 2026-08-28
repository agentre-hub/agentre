$ErrorActionPreference = "Stop"

function Fail([string]$Message) {
    throw "agentred installer: $Message"
}

$ReleaseBaseUrl = if ($env:AGENTRED_RELEASE_BASE_URL) {
    $env:AGENTRED_RELEASE_BASE_URL.TrimEnd('/')
} else {
    "https://github.com/agentre-hub/agentre/releases/latest/download"
}
$Arch = switch ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
    "X64" { "amd64" }
    "Arm64" { "arm64" }
    default { Fail "unsupported architecture: $_" }
}
if (-not $env:LOCALAPPDATA -and -not $env:AGENTRED_INSTALL_DIR) {
    Fail "LOCALAPPDATA is required for user-level installation"
}
$InstallDir = if ($env:AGENTRED_INSTALL_DIR) {
    $env:AGENTRED_INSTALL_DIR
} else {
    Join-Path $env:LOCALAPPDATA "Programs\agentred"
}

$WorkDir = Join-Path ([System.IO.Path]::GetTempPath()) ("agentred-install-" + [guid]::NewGuid())
try {
    New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
    $ChecksumsPath = Join-Path $WorkDir "SHA256SUMS"
    Invoke-WebRequest -UseBasicParsing -Uri "$ReleaseBaseUrl/SHA256SUMS" -OutFile $ChecksumsPath

    $AssetPattern = "^([0-9a-fA-F]{64})\s+\*?(agentred-.*-windows-$Arch\.zip)$"
    $Checksum = $null
    $Asset = $null
    foreach ($Line in Get-Content -LiteralPath $ChecksumsPath) {
        if ($Line -match $AssetPattern) {
            $Checksum = $Matches[1].ToLowerInvariant()
            $Asset = $Matches[2]
            break
        }
    }
    if (-not $Asset) { Fail "SHA256SUMS does not contain an agentred archive for windows/$Arch" }

    $ArchivePath = Join-Path $WorkDir $Asset
    Invoke-WebRequest -UseBasicParsing -Uri "$ReleaseBaseUrl/$Asset" -OutFile $ArchivePath
    $Actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $ArchivePath).Hash.ToLowerInvariant()
    if ($Actual -ne $Checksum) { Fail "SHA256 checksum mismatch for $Asset" }

    $ExtractDir = Join-Path $WorkDir "extracted"
    Expand-Archive -LiteralPath $ArchivePath -DestinationPath $ExtractDir
    $ExtractedBinary = Join-Path $ExtractDir "agentred.exe"
    if (-not (Test-Path -LiteralPath $ExtractedBinary -PathType Leaf)) { Fail "archive does not contain agentred.exe" }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $InstalledBinary = Join-Path $InstallDir "agentred.exe"
    Copy-Item -LiteralPath $ExtractedBinary -Destination $InstalledBinary -Force

    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $UserEntries = if ($UserPath) { $UserPath -split ';' } else { @() }
    if ($UserEntries -notcontains $InstallDir) {
        $UpdatedUserPath = if ($UserPath) { "$UserPath;$InstallDir" } else { $InstallDir }
        [Environment]::SetEnvironmentVariable("Path", $UpdatedUserPath, "User")
    }
    if (($env:Path -split ';') -notcontains $InstallDir) {
        $env:Path = "$InstallDir;$env:Path"
    }

    $Identity = (& $InstalledBinary --version | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) { Fail "installed binary failed --version confirmation" }
    if ($Identity -notlike "agentred *") { Fail "installed binary returned an unexpected version: $Identity" }
    Write-Output "Installed agentred to $InstalledBinary"
    Write-Output $Identity
    Write-Output "Added $InstallDir to the user PATH. Open a new terminal to use agentred."
} finally {
    Remove-Item -LiteralPath $WorkDir -Recurse -Force -ErrorAction SilentlyContinue
}
