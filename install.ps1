# Installs the giode CLI on Windows:
#
#   irm https://raw.githubusercontent.com/xmhrnx/giode-cli/main/install.ps1 | iex
#
# It downloads the latest release (or $env:GIODE_VERSION, like v0.2.0)
# into %LOCALAPPDATA%\Programs\giode (or $env:GIODE_INSTALL_DIR) and adds
# that folder to your PATH.
$ErrorActionPreference = 'Stop'
$repo = 'xmhrnx/giode-cli'
$arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'arm64' } else { 'amd64' }
$name = "giode_windows_$arch"
$version = $env:GIODE_VERSION
if (-not $version) {
  # The newest release with archives, pre-releases included: GitHub's
  # releases/latest leaves those out, and giode's are alphas for now.
  $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases?per_page=20" -UseBasicParsing
  $release = $releases | Where-Object { -not $_.draft -and ($_.assets | Where-Object { $_.name -eq "$name.zip" }) } | Select-Object -First 1
  if (-not $release) {
    throw "No giode release for Windows $arch is published yet; install with: go install github.com/xmhrnx/giode-cli/cmd/giode@latest"
  }
  $version = $release.tag_name
}
$url = "https://github.com/$repo/releases/download/$version/$name.zip"
$dir = if ($env:GIODE_INSTALL_DIR) { $env:GIODE_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\giode' }
New-Item -ItemType Directory -Force -Path $dir | Out-Null

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("giode-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "Downloading $url"
  Invoke-WebRequest -Uri $url -OutFile (Join-Path $tmp 'giode.zip') -UseBasicParsing
  Expand-Archive -Path (Join-Path $tmp 'giode.zip') -DestinationPath $tmp -Force
  Copy-Item -Force (Join-Path $tmp 'giode.exe') (Join-Path $dir 'giode.exe')
} finally {
  Remove-Item -Recurse -Force $tmp
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not ($userPath -split ';' | Where-Object { $_ -eq $dir })) {
  [Environment]::SetEnvironmentVariable('Path', ($userPath.TrimEnd(';') + ';' + $dir), 'User')
  $env:Path += ";$dir"
  Write-Host "Added $dir to your PATH; open a new terminal to use giode there."
}
& (Join-Path $dir 'giode.exe') version
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  Write-Host 'giode builds apps with Go 1.26 or later: install it from https://go.dev/dl'
}
