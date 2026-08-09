@echo off
setlocal
cd /d "%~dp0"
echo Building release...
set CGO_ENABLED=0

rem Refresh Windows icon/version resources when go-winres is available.
where go-winres >nul 2>nul
if not errorlevel 1 (
  if exist winres\winres.json (
    go run .\tools\genico.go .\assets\icon.png
    go-winres make --in winres\winres.json --out rsrc --arch amd64
  )
)

go build -trimpath -ldflags="-H windowsgui -s -w" -o publish\Trace.exe .
if errorlevel 1 (
  echo Build failed.
  exit /b 1
)
start "" "publish\Trace.exe"
