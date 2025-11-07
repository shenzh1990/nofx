# 使用一个轻量级基础镜像
FROM alpine:latest

# 安装并设置时区
RUN apk add --no-cache tzdata \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone

# 设置时区 & 默认环境变量（可被 docker run -e 或 K8s 覆盖）
ENV TZ=Asia/Shanghai \
    NOFX_ADMIN_PASSWORD=admin

# 设置工作目录
WORKDIR /nofx-backend

# 把编译好的二进制复制进去
COPY ./nofx .

RUN mkdir -p /data
COPY ./config.json ./data/
COPY ./beta_codes.txt ./data/
RUN chmod +x nofx


# 开放端口（如果你的程序监听端口，比如 8080）
EXPOSE 8080

# 执行
CMD ["./nofx"]
