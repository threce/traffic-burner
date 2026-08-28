# ---------- build stage ----------
FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY web ./web
# 静态编译，方便跑在最小镜像里
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/traffic-burner .

# ---------- runtime stage ----------
# 用最精简的基础镜像，但需要复制 CA 根证书，否则无法验证 api.telegram.org 的 TLS
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/traffic-burner /traffic-burner
EXPOSE 8080
# 端口、用户名、密码均通过环境变量注入（见 docker-compose 或直接 docker run -e）
ENTRYPOINT ["/traffic-burner"]
