@echo off
rem Windows E2E bootstrap: download the bundle over HTTP and run it.
rem Usage: bootstrap.cmd [base_url]
setlocal
set "ROOT=%USERPROFILE%\sesscope-e2e"
set "BASE_URL=%~1"
if "%BASE_URL%"=="" set "BASE_URL=http://172.16.22.201:18081"
curl.exe -s -o "%ROOT%.tgz" "%BASE_URL%/sesscope-e2e.tgz"
if errorlevel 1 goto fail
if exist "%ROOT%" rmdir /s /q "%ROOT%"
mkdir "%ROOT%"
tar -xzf "%ROOT%.tgz" -C "%ROOT%"
if errorlevel 1 goto fail
call "%ROOT%\run-e2e.cmd" "%BASE_URL%"
exit /b 0
:fail
echo BOOTSTRAP_FAILED
pause
