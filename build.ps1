# 设置环境变量
$env:GOOS="linux"
$env:GOARCH="amd64"

# 执行构建命令
go build -o ./bin/linux64/nofx main.go
