@echo off
setlocal enabledelayedexpansion

:: data_factory 启动脚本 (Windows)
:: 用法: start.bat [端口]   默认端口 8080

set PORT=8080
if not "%~1"=="" set PORT=%~1

echo.
echo  ╔══════════════════════════════════╗
echo  ║    data_factory  启动中          ║
echo  ╚══════════════════════════════════╝
echo    端口 : %PORT%
echo    地址 : http://localhost:%PORT%
echo.

:: ── 检查端口是否被占用（用临时文件避免 for/f 管道嵌套问题）──
set "DF_TMP=%TEMP%\df_port_%RANDOM%.tmp"
netstat -aon | findstr ":%PORT% " | findstr "LISTENING" > "%DF_TMP%" 2>nul

set "FOUND_PID="
for /f "tokens=5" %%a in (%DF_TMP%) do (
    if not defined FOUND_PID set FOUND_PID=%%a
)
del "%DF_TMP%" >nul 2>&1

if defined FOUND_PID (
    echo   ^⚠️  端口 %PORT% 已被占用 ^(PID: !FOUND_PID!^)，正在终止...
    taskkill /F /PID !FOUND_PID! >nul 2>&1
    timeout /t 1 /nobreak >nul
    echo   ^✅  端口 %PORT% 已释放
    echo.
)

echo   启动 data_factory...
"%~dp0data_factory.exe" -port %PORT%

endlocal
