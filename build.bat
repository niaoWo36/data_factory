@echo off
:: build.bat – 跨平台打包脚本 (Windows)
:: 用法:
::   build.bat              打包所有平台
::   build.bat win          仅打包 Windows (amd64)
::   build.bat mac          仅打包 macOS (arm64 + amd64)
::   build.bat linux        仅打包 Linux (amd64)
::   build.bat current      仅打包当前平台
::   build.bat clean        删除 dist\ 目录

setlocal enabledelayedexpansion

set APP_NAME=data_factory
set DIST_DIR=dist
set TARGET=%~1
if "%TARGET%"=="" set TARGET=all

:: Get version from git (fallback to "dev")
for /f "delims=" %%i in ('git describe --tags --always --dirty 2^>nul') do set VERSION=%%i
if "%VERSION%"=="" set VERSION=dev

:: Build timestamp
for /f "tokens=1-6 delims=/:. " %%a in ('echo %date% %time%') do (
    set BUILD_TIME=%%a-%%b-%%cT%%d:%%e:%%fZ
)

set LDFLAGS=-s -w -X main.Version=%VERSION% -X main.BuildTime=%BUILD_TIME%

echo.
echo ╔═══════════════════════════════════════════╗
echo ║  data_factory 打包工具  v%VERSION%
echo ╚═══════════════════════════════════════════╝
echo   时间: %BUILD_TIME%
echo.

if not exist %DIST_DIR% mkdir %DIST_DIR%

if "%TARGET%"=="clean"   goto :clean
if "%TARGET%"=="win"     goto :build_win
if "%TARGET%"=="mac"     goto :build_mac
if "%TARGET%"=="linux"   goto :build_linux
if "%TARGET%"=="current" goto :build_current
if "%TARGET%"=="all"     goto :build_all

echo 未知目标: %TARGET%
exit /b 1

:build_all
call :build_win
call :build_mac
call :build_linux
goto :done

:build_win
set OUTDIR=%DIST_DIR%\data-windows-amd64
if exist %OUTDIR% ( rmdir /s /q %OUTDIR% & echo   已清理旧目录: %OUTDIR% )
mkdir %OUTDIR%
echo   [BUILD] GOOS=windows GOARCH=amd64
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -trimpath -ldflags "%LDFLAGS%" -o "%OUTDIR%\%APP_NAME%.exe" .
if errorlevel 1 ( echo   ❌ Windows 构建失败 & exit /b 1 )
echo   ✅ %OUTDIR%\%APP_NAME%.exe
copy /y scripts\start.bat "%OUTDIR%\start.bat" >nul
echo   ✅ 启动脚本: %OUTDIR%\start.bat
goto :eof

:build_mac
set OUTDIR=%DIST_DIR%\data-macos-arm64
if exist %OUTDIR% ( rmdir /s /q %OUTDIR% & echo   已清理旧目录: %OUTDIR% )
mkdir %OUTDIR%
echo   [BUILD] GOOS=darwin GOARCH=arm64
set CGO_ENABLED=0
set GOOS=darwin
set GOARCH=arm64
go build -trimpath -ldflags "%LDFLAGS%" -o "%OUTDIR%\%APP_NAME%" .
if errorlevel 1 ( echo   ❌ macOS arm64 构建失败 & exit /b 1 )
echo   ✅ %OUTDIR%\%APP_NAME%
copy /y scripts\start.sh "%OUTDIR%\start.sh" >nul
echo   ✅ 启动脚本: %OUTDIR%\start.sh

set OUTDIR=%DIST_DIR%\data-macos-amd64
if exist %OUTDIR% ( rmdir /s /q %OUTDIR% & echo   已清理旧目录: %OUTDIR% )
mkdir %OUTDIR%
echo   [BUILD] GOOS=darwin GOARCH=amd64
set GOARCH=amd64
go build -trimpath -ldflags "%LDFLAGS%" -o "%OUTDIR%\%APP_NAME%" .
if errorlevel 1 ( echo   ❌ macOS amd64 构建失败 & exit /b 1 )
echo   ✅ %OUTDIR%\%APP_NAME%
copy /y scripts\start.sh "%OUTDIR%\start.sh" >nul
echo   ✅ 启动脚本: %OUTDIR%\start.sh
goto :eof

:build_linux
set OUTDIR=%DIST_DIR%\data-linux-amd64
if exist %OUTDIR% ( rmdir /s /q %OUTDIR% & echo   已清理旧目录: %OUTDIR% )
mkdir %OUTDIR%
echo   [BUILD] GOOS=linux GOARCH=amd64
set CGO_ENABLED=0
set GOOS=linux
set GOARCH=amd64
go build -trimpath -ldflags "%LDFLAGS%" -o "%OUTDIR%\%APP_NAME%" .
if errorlevel 1 ( echo   ❌ Linux 构建失败 & exit /b 1 )
echo   ✅ %OUTDIR%\%APP_NAME%
copy /y scripts\start.sh "%OUTDIR%\start.sh" >nul
echo   ✅ 启动脚本: %OUTDIR%\start.sh
goto :eof

:build_current
echo   [BUILD] 当前平台
set CGO_ENABLED=0
set GOOS=
set GOARCH=
set OUTDIR=%DIST_DIR%\data-current
if exist %OUTDIR% ( rmdir /s /q %OUTDIR% & echo   已清理旧目录: %OUTDIR% )
mkdir %OUTDIR%
go build -trimpath -ldflags "%LDFLAGS%" -o "%OUTDIR%\%APP_NAME%.exe" .
if errorlevel 1 ( echo   ❌ 当前平台构建失败 & exit /b 1 )
echo   ✅ %OUTDIR%\%APP_NAME%.exe
copy /y scripts\start.bat "%OUTDIR%\start.bat" >nul
echo   ✅ 启动脚本: %OUTDIR%\start.bat
goto :done

:clean
echo   🗑️  清理 %DIST_DIR%\
rmdir /s /q %DIST_DIR% 2>nul
echo   dist\ 已清理
goto :eof

:done
echo.
echo 📦 输出目录: %DIST_DIR%\
dir /b %DIST_DIR%\ 2>nul | findstr /r ".*" && echo. || echo   (空)
echo.
echo ✨ 打包完成
endlocal
