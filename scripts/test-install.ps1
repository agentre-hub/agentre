$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent $PSScriptRoot
$Installer = Join-Path $PSScriptRoot "install.ps1"
if (-not (Test-Path -LiteralPath $Installer -PathType Leaf)) {
    throw "missing installer: $Installer"
}

$Version = "v9.8.7"
$Commit = "abcdef1234567890"
$Identity = "agentred $Version (abcdef1)"
$GoArch = switch ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
    "X64" { "amd64" }
    "Arm64" { "arm64" }
    default { throw "unsupported test architecture: $_" }
}
$Asset = "agentred-$Version-windows-$GoArch.zip"
$TempRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("agentred-installer-test-" + [guid]::NewGuid())
$ReleaseDir = Join-Path $TempRoot "release"
$ArchiveDir = Join-Path $TempRoot "archive"
$OriginalUserPath = [Environment]::GetEnvironmentVariable("Path", "User")
$Server = $null

try {
    New-Item -ItemType Directory -Force -Path $ReleaseDir, $ArchiveDir | Out-Null
    $FixtureBinary = Join-Path $ArchiveDir "agentred.exe"
    Push-Location $RepoRoot
    try {
        & go build "-ldflags=-s -w -X github.com/cago-frame/cago/configs.Version=$Version -X github.com/agentre-hub/agentre/internal/buildinfo.CommitID=$Commit" -o $FixtureBinary ./cmd/agentred
        if ($LASTEXITCODE -ne 0) { throw "fixture agentred build failed" }
    } finally {
        Pop-Location
    }

    $Archive = Join-Path $ReleaseDir $Asset
    Compress-Archive -LiteralPath $FixtureBinary -DestinationPath $Archive
    $Digest = (Get-FileHash -Algorithm SHA256 -LiteralPath $Archive).Hash.ToLowerInvariant()
    Set-Content -LiteralPath (Join-Path $ReleaseDir "SHA256SUMS") -Value "$Digest  $Asset" -Encoding ascii

    $Listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $Listener.Start()
    $Port = ([System.Net.IPEndPoint]$Listener.LocalEndpoint).Port
    $Listener.Stop()
    $Server = Start-Process -FilePath python -ArgumentList @("-m", "http.server", "$Port", "--bind", "127.0.0.1") -WorkingDirectory $ReleaseDir -PassThru -WindowStyle Hidden
    $BaseUrl = "http://127.0.0.1:$Port"
    $Ready = $false
    foreach ($Attempt in 1..50) {
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$BaseUrl/SHA256SUMS" | Out-Null
            $Ready = $true
            break
        } catch {
            Start-Sleep -Milliseconds 100
        }
    }
    if (-not $Ready) { throw "fixture HTTP server did not start" }

    $env:AGENTRED_RELEASE_BASE_URL = $BaseUrl
    $env:LOCALAPPDATA = Join-Path $TempRoot "LocalAppData"
    Remove-Item Env:AGENTRED_INSTALL_DIR -ErrorAction SilentlyContinue
    $Output = (& $Installer 2>&1 | Out-String)
    $InstalledDir = Join-Path $env:LOCALAPPDATA "Programs\agentred"
    $InstalledBinary = Join-Path $InstalledDir "agentred.exe"
    if (-not (Test-Path -LiteralPath $InstalledBinary -PathType Leaf)) { throw "agentred.exe was not installed to the user-level destination" }
    $Reported = (& $InstalledBinary --version | Out-String).Trim()
    if ($Reported -ne $Identity) { throw "unexpected installed identity: $Reported" }
    if (-not $Output.Contains($Identity)) { throw "installer did not confirm --version output" }
    $UpdatedUserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (($UpdatedUserPath -split ';') -notcontains $InstalledDir) { throw "installer did not add its user-level destination to user PATH" }

    Set-Content -LiteralPath (Join-Path $ReleaseDir "SHA256SUMS") -Value (('0' * 64) + "  $Asset") -Encoding ascii
    $env:AGENTRED_INSTALL_DIR = Join-Path $TempRoot "mismatch-bin"
    $Rejected = $false
    try {
        & $Installer *> (Join-Path $TempRoot "mismatch.out")
    } catch {
        $Rejected = $_.Exception.Message -match "checksum|SHA-?256"
    }
    if (-not $Rejected) { throw "installer did not reject a checksum mismatch with a checksum error" }
    if (Test-Path -LiteralPath (Join-Path $env:AGENTRED_INSTALL_DIR "agentred.exe")) { throw "mismatched binary was installed" }

    Write-Output "install.ps1 fixture tests passed"
} finally {
    [Environment]::SetEnvironmentVariable("Path", $OriginalUserPath, "User")
    if ($null -ne $Server -and -not $Server.HasExited) {
        Stop-Process -Id $Server.Id -Force -ErrorAction SilentlyContinue
    }
    Remove-Item Env:AGENTRED_RELEASE_BASE_URL -ErrorAction SilentlyContinue
    Remove-Item Env:AGENTRED_INSTALL_DIR -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $TempRoot -Recurse -Force -ErrorAction SilentlyContinue
}
