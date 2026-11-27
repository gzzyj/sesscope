@echo off
setlocal EnableExtensions
chcp 65001 >nul
set "ROOT=%USERPROFILE%\sesscope-e2e"
set "BASE_URL=%~1"
if "%BASE_URL%"=="" set "BASE_URL=http://172.16.22.201:18081"
set "SRC=%~dp0"
set "EXE=%SRC%sesscope.exe"
set "FIX=%SRC%fixtures"
set "OUT=%ROOT%\out"
if exist "%OUT%" rmdir /s /q "%OUT%"
mkdir "%OUT%"
echo [1/6] seed fixture stores into %USERPROFILE%
if not exist "%USERPROFILE%\.dsh" mkdir "%USERPROFILE%\.dsh"
if not exist "%USERPROFILE%\.codex" mkdir "%USERPROFILE%\.codex"
if not exist "%USERPROFILE%\.claude" mkdir "%USERPROFILE%\.claude"
if not exist "%LOCALAPPDATA%\opencode" mkdir "%LOCALAPPDATA%\opencode"
xcopy /E /I /Y "%FIX%\home\.dsh" "%USERPROFILE%\.dsh" >nul
xcopy /E /I /Y "%FIX%\home\.codex" "%USERPROFILE%\.codex" >nul
xcopy /E /I /Y "%FIX%\home\.claude" "%USERPROFILE%\.claude" >nul
copy /Y "%FIX%\home\AppData\Local\opencode\opencode.db" "%LOCALAPPDATA%\opencode\opencode.db" >nul
echo [2/6] start a real opencode sidecar for process discovery
set "SIDECAR=%LOCALAPPDATA%\Programs\DeepWorks\resources\sidecars\opencode-x86_64-pc-windows-msvc.exe"
if exist "%SIDECAR%" start "" "%SIDECAR%" serve --hostname 127.0.0.1 --port 49952
timeout /t 3 /nobreak >nul
set "OPENCODE_PID="
for /f "tokens=2 delims=," %%p in ('tasklist /FO CSV /NH ^| findstr /I "opencode"') do if not defined OPENCODE_PID set "OPENCODE_PID=%%~p"
echo opencode_pid=%OPENCODE_PID% > "%OUT%\00-process.txt"
echo [3/6] discovery commands
"%EXE%" version --json > "%OUT%\01-version.json" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\01-version.exit"
"%EXE%" sources --explain > "%OUT%\02-sources.txt" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\02-sources.exit"
"%EXE%" sources --json > "%OUT%\03-sources.json" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\03-sources.exit"
if not defined OPENCODE_PID goto after_pid
"%EXE%" sources --source opencode --pid %OPENCODE_PID% --explain > "%OUT%\04-sources-pid.txt" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\04-sources-pid.exit"
:after_pid
"%EXE%" workspaces > "%OUT%\05-workspaces.txt" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\05-workspaces.exit"
set "DEEPWORKS_CONFIG_HOME=%FIX%\deepworks"
"%EXE%" workspaces > "%OUT%\06-workspaces-appstate.txt" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\06-workspaces-appstate.exit"
set "DEEPWORKS_CONFIG_HOME="
"%EXE%" sessions --limit 20 > "%OUT%\07-sessions.txt" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\07-sessions.exit"
"%EXE%" sessions --limit 8 --json > "%OUT%\08-sessions.json" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\08-sessions.exit"
"%EXE%" doctor --drift > "%OUT%\09-doctor.txt" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\09-doctor.exit"
echo [4/6] export / analyze / render
"%EXE%" export --session opencode:ses_root0000000000000000000000000001 --out "%OUT%\10-opencode.json" --html "%OUT%\10-opencode.html" --data "%OUT%\10-opencode.data.json" > "%OUT%\10-opencode.log" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\10-opencode.exit"
"%EXE%" export --session dsh:session-wine2e-0001 --out "%OUT%\11-dsh.json" --html "%OUT%\11-dsh.html" > "%OUT%\11-dsh.log" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\11-dsh.exit"
"%EXE%" export --session codex:01a0c89f-3c06-73c3-aae8-e6de46100531 --out "%OUT%\12-codex.json" --html "%OUT%\12-codex.html" > "%OUT%\12-codex.log" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\12-codex.exit"
"%EXE%" export --session claude:11111111-2222-4333-8444-555555555555 --out "%OUT%\13-claude.json" --html "%OUT%\13-claude.html" > "%OUT%\13-claude.log" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\13-claude.exit"
"%EXE%" analyze --session dsh:session-wine2e-0001 --json > "%OUT%\15-analyze.json" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\15-analyze.exit"
"%EXE%" analyze --in "%OUT%\11-dsh.json" > "%OUT%\16-analyze-in.txt" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\16-analyze-in.exit"
"%EXE%" render --in "%OUT%\10-opencode.json" --out "%OUT%\17-render.html" --data "%OUT%\17-render.data.json" > "%OUT%\17-render.log" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\17-render.exit"
echo [5/6] interactive ui (piped keys, --no-browser)
(echo 1& echo a& echo 1& echo o& echo.& echo q) | "%EXE%" ui --source opencode --no-browser > "%OUT%\18-ui.txt" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\18-ui.exit"
for %%f in ("%TEMP%\sesscope-opencode-*.html") do copy /Y "%%~f" "%OUT%\18-ui.html" >nul
"%EXE%" sources --source dsh --home "%FIX%\home\.dsh" --explain > "%OUT%\19-home.txt" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\19-home.exit"
"%EXE%" sessions --source claude --store "%FIX%\home\.claude\projects" --limit 3 --json > "%OUT%\20-store.json" 2>&1
set "CODE=%ERRORLEVEL%"
echo %CODE% > "%OUT%\20-store.exit"
taskkill /IM opencode-x86_64-pc-windows-msvc.exe /F >nul 2>&1
taskkill /IM opencode-router-x86_64-pc-windows-msvc.exe /F >nul 2>&1
echo [6/6] package and upload results
tar -czf "%ROOT%\results.tgz" -C "%ROOT%" out
set "TAR_CODE=%ERRORLEVEL%"
echo tar_exit=%TAR_CODE%
curl.exe -s -T "%ROOT%\results.tgz" "%BASE_URL%/upload/windows-e2e-results.tgz"
echo upload_exit=%ERRORLEVEL%
echo E2E_DONE
endlocal
