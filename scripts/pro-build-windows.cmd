@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0pro-build-windows.ps1"
if errorlevel 1 (
  echo.
  echo ccNexus Pro build failed.
  pause
)
