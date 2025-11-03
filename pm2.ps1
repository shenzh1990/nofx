# 设置 UTF-8 编码
$OutputEncoding = New-Object -typename System.Text.UTF8Encoding
[Console]::OutputEncoding = New-Object -typename System.Text.UTF8Encoding

# 检查 PM2 是否安装
if (!(Get-Command pm2 -ErrorAction SilentlyContinue)) {
    Write-Host "PM2 not install please install PM2: npm install -g pm2" -ForegroundColor Red
    exit 1
}

# 获取项目根目录
$PROJECT_ROOT = Get-Location

# 确保日志目录存在
New-Item -ItemType Directory -Path "$PROJECT_ROOT\logs" -Force | Out-Null
New-Item -ItemType Directory -Path "$PROJECT_ROOT\web\logs" -Force | Out-Null

# 根据参数执行相应操作
switch ($args[0]) {
    "start" {
            # 检查后端二进制文件
            if (!(Test-Path "$PROJECT_ROOT\nofx.exe")) {
                Write-Host "combile backend..." -ForegroundColor Yellow
                go build -o nofx.exe
                if ($LASTEXITCODE -ne 0) {
                    Write-Host "combile backend fail:" -ForegroundColor Red
                    exit 1
                }
            }

            # 确保日志目录存在
            New-Item -ItemType Directory -Path "$PROJECT_ROOT\logs" -Force | Out-Null
            New-Item -ItemType Directory -Path "$PROJECT_ROOT\web\logs" -Force | Out-Null

            pm2 start pm2.config.js
            Start-Sleep -Seconds 2
            pm2 status
            Write-Host ""
            Write-Host "success" -ForegroundColor Green
            Write-Host ""
            Write-Host "URL:" -ForegroundColor Cyan
            Write-Host "Frontend-Web: http://localhost:3000" -ForegroundColor Green
            Write-Host "Backend-API: http://localhost:8080" -ForegroundColor Green
            Write-Host ""
            Write-Host "show Logs:" -ForegroundColor Cyan
            Write-Host "logs: .\pm2.ps1 logs" -ForegroundColor Green
            Write-Host "backend logs: .\pm2.ps1 logs backend" -ForegroundColor Green
            Write-Host "frontedn logs: .\pm2.ps1 logs frontend" -ForegroundColor Green
            Write-Host ""
            Write-Host "stop service:" -ForegroundColor Cyan
            Write-Host "  .\pm2.ps1 stop" -ForegroundColor Green
            Write-Host "  .\pm2.ps1 stop backend" -ForegroundColor Green
            Write-Host "  .\pm2.ps1 stop frontend" -ForegroundColor Green
            Write-Host ""
    }
    "stop" {
        pm2 stop pm2.config.js
    }
    "restart" {
        pm2 restart pm2.config.js
        Start-Sleep -Seconds 2
        pm2 status
    }
    "status" {
        pm2 status
    }
    "logs" {
        if ($args.Count -gt 1) {
            pm2 logs $args[1]
        } else {
            pm2 logs
        }
    }
    default {
        Write-Host "use pm2.ps1: ./pm2.ps1 [start|stop|restart|status|logs]" -ForegroundColor Cyan
    }
}
