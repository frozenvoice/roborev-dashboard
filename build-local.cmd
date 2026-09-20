@echo off
setlocal EnableExtensions
set "ROOT=%~dp0"

rem This wrapper intentionally uses the Windows PowerShell 5.1 shipped with
rem Windows so a fresh Windows 11 checkout does not need another shell first.
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "%ROOT%scripts\build-local.ps1" %*
set "ERR=%ERRORLEVEL%"
if not "%ERR%"=="0" (
    echo.
    if "%ERR%"=="2" (
        echo RoboRev Dashboard installation was canceled before changes began ^(exit 2^).
    ) else (
        echo RoboRev Dashboard local build failed ^(exit %ERR%^).
    )
    echo See the error above. Logs and per-run staging are under "%ROOT%dist\local-build".
    echo.
    pause
)
endlocal & exit /b %ERR%
