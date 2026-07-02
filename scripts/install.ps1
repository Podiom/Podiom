param(
  [string]$Version = "latest",
  [string]$InstallDir = "$env:LOCALAPPDATA\Podiom\bin",
  [string]$PodiomHome = "",
  [ValidateSet("ask", "yes", "no")]
  [string]$Autostart = "ask",
  [switch]$NoOnboard,
  [switch]$SourceFallback,
  [switch]$DryRun
)

$ErrorActionPreference = "Stop"
$ReleaseBase = if ($env:PODIOM_RELEASE_BASE) { $env:PODIOM_RELEASE_BASE } else { "https://github.com/Podiom/Podiom/releases" }
$RepoUrl = if ($env:PODIOM_REPO_URL) { $env:PODIOM_REPO_URL } else { "https://github.com/Podiom/Podiom.git" }

function Say($Message) { Write-Host $Message }
function Invoke-Step([scriptblock]$Block, [string]$Description) {
  if ($DryRun) {
    Write-Host "[dry-run] $Description"
  } else {
    & $Block
  }
}

$arch = switch ((Get-CimInstance Win32_OperatingSystem).OSArchitecture) {
  { $_ -match "ARM64" } { "arm64"; break }
  default { "amd64" }
}

if ($Version -eq "latest") {
  $releaseUrl = "$ReleaseBase/latest/download"
  $archive = "podiom_windows_$arch.zip"
} else {
  $releaseUrl = "$ReleaseBase/download/$Version"
  $archive = "podiom_${Version}_windows_$arch.zip"
}
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("podiom-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

try {
  $archivePath = Join-Path $tmp $archive
  $sumPath = Join-Path $tmp "SHA256SUMS"
  $url = "$releaseUrl/$archive"
  $sumUrl = "$releaseUrl/SHA256SUMS"

  $downloadOk = $false
  try {
    Say "Downloading $url"
    if ($DryRun) {
      Say "[dry-run] Invoke-WebRequest $url -OutFile $archivePath"
      Say "[dry-run] Invoke-WebRequest $sumUrl -OutFile $sumPath"
      Invoke-Step { New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null } "create $InstallDir"
      Say "[dry-run] verify checksum and unpack $archive"
      Say "[dry-run] install podiom.exe and podiomd.exe into $InstallDir"
    } else {
      Invoke-WebRequest -Uri $url -OutFile $archivePath
      Invoke-WebRequest -Uri $sumUrl -OutFile $sumPath
      $line = Get-Content $sumPath | Where-Object { $_ -match [regex]::Escape($archive) } | Select-Object -First 1
      if (-not $line) { throw "checksum entry for $archive not found" }
      $expected = ($line -split "\s+")[0].ToLowerInvariant()
      $actual = (Get-FileHash -Algorithm SHA256 $archivePath).Hash.ToLowerInvariant()
      if ($expected -ne $actual) { throw "checksum mismatch: expected $expected, got $actual" }
      $unpack = Join-Path $tmp "unpack"
      Expand-Archive -Path $archivePath -DestinationPath $unpack -Force
      Invoke-Step { New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null } "create $InstallDir"
      Invoke-Step { Copy-Item (Join-Path $unpack "podiom.exe") (Join-Path $InstallDir "podiom.exe") -Force } "install podiom.exe"
      Invoke-Step { Copy-Item (Join-Path $unpack "podiomd.exe") (Join-Path $InstallDir "podiomd.exe") -Force } "install podiomd.exe"
    }
    $downloadOk = $true
  } catch {
    if (-not $SourceFallback) {
      throw "Release download failed: $_. Re-run with -SourceFallback to build locally."
    }
  }

  if (-not $downloadOk) {
    Say "Building Podiom from source fallback."
    $work = Join-Path $tmp "src"
    if ((Test-Path "go.mod") -and (Test-Path "cmd\podiom")) {
      $work = (Get-Location).Path
    } else {
      git clone --depth 1 $RepoUrl $work
    }
    Push-Location $work
    try { make build } finally { Pop-Location }
    Invoke-Step { New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null } "create $InstallDir"
    Invoke-Step { Copy-Item (Join-Path $work "bin\podiom.exe") (Join-Path $InstallDir "podiom.exe") -Force } "install podiom.exe"
    Invoke-Step { Copy-Item (Join-Path $work "bin\podiomd.exe") (Join-Path $InstallDir "podiomd.exe") -Force } "install podiomd.exe"
  }

  if ($PodiomHome) {
    $env:PODIOM_HOME = $PodiomHome
  }

  if ($Autostart -eq "ask") {
    $reply = Read-Host "Start Podiom automatically when your system starts? [Y/n]"
    if ($reply -and $reply.ToLowerInvariant().StartsWith("n")) { $Autostart = "no" } else { $Autostart = "yes" }
  }

  if ($Autostart -eq "yes") {
    $podiomd = Join-Path $InstallDir "podiomd.exe"
    Invoke-Step {
      if ($PodiomHome) {
        $quotedHome = $PodiomHome.Replace("'", "''")
        $quotedExe = $podiomd.Replace("'", "''")
        $action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -WindowStyle Hidden -Command `$env:PODIOM_HOME='$quotedHome'; & '$quotedExe'"
      } else {
        $action = New-ScheduledTaskAction -Execute $podiomd
      }
      $trigger = New-ScheduledTaskTrigger -AtLogOn
      $principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive
      Register-ScheduledTask -TaskName "Podiom" -Action $action -Trigger $trigger -Principal $principal -Description "Start Podiom daemon at logon" -Force | Out-Null
    } "register current-user Scheduled Task: Podiom"
  }

  Say "Podiom installed to $InstallDir."
  if (-not ($env:Path.Split(';') -contains $InstallDir)) {
    Say "Add $InstallDir to PATH if PowerShell cannot find podiom."
  }
  if (-not $NoOnboard) {
    $podiom = Join-Path $InstallDir "podiom.exe"
    Invoke-Step { & $podiom onboard } "run podiom onboard"
  }
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
