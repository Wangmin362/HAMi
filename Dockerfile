FROM ubuntu:22.04

# 构建命令：docker build -t hami-wm:2.4.1 .
COPY bin/ /usr/local/bin
COPY dlv /usr/local/bin