@echo off
rem grscan 重新编译脚本：双击即可，或命令行运行
cd /d "%~dp0"

where go >nul 2>nul
if errorlevel 1 (
    echo [x] 没找到 go，请确认 Go 已安装并在 PATH 中
    echo     https://go.dev/dl/
    pause
    exit /b 1
)

go build -trimpath -ldflags="-s -w" -o igit.exe .
if errorlevel 1 (
    echo.
    echo [x] 编译失败
    pause
    exit /b 1
)
@REM copy /y igit.exe grscan.exe >nul 2>nul

echo.
echo [ok] 已生成 %CD%\igit.exe
dir /b igit.exe
echo.
pause
