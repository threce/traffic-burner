# 🔥 Traffic Burner 流量消耗器

一个可部署在云服务器上的内存型流量消耗器：**不写硬盘、多线程**、可自定义消耗**上传**或**下载**流量、可自定义消耗量，通过浏览器页面或服务端直发来快速跑满云服务器的带宽。

> ⚠️ 请仅用于消耗自己购买、用不完的流量。滥用可能违反云厂商服务条款，后果自负。

## 功能特性

- **内存直发，不占硬盘**：数据在内存中生成，循环发送、直接丢弃，全程不写磁盘。
- **上传 / 下载 / 双向 三种方向**：按云厂商计费口径区分出站(上传)与入站(下载)流量。
- **多线程并发**：浏览器端支持最多 64 线程，服务端直发支持最多 64 条 TCP 连接。
- **自定义消耗量**：可设固定总量（MB/GB）或不限时长直到手动停止。
- **服务端直发**：服务器主动向目标 host:port 建立连接发送数据，可后台无人值守、关闭页面也不中断。
- **多机互打**：配合多台服务器，可互相指向对方公网 IP 实现超高带宽互发。
- **安全登录**：用户名 + 密码 + **Telegram 动态验证码** 三重验证；未配置 TG 时降级为账密登录。
- **TG 指令控制**：绑定 Telegram Bot 后，可在聊天窗口用指令查看统计/启动/停止消耗、清零等。
- **实时统计**：Web 页面每秒刷新上传/下载总量、实时速率、活跃连接数。

## 技术原理

`traffic-burner` 用内置随机数据缓冲（16MB，纯内存），以循环写入/读取流的方式产生真实网络 IO。下表对应关系（以「用户选择的消耗方向」为基准）：

| 你选择消耗 | 浏览器实际动作 | 服务器计费口径 |
|------|------|----------|
| ⬆ 上传流量（服务器→公网） | 浏览器从服务器**下载**内存流 | 服务器**出站**(上传)流量 |
| ⬇ 下载流量（公网→服务器） | 浏览器向服务器**上传**内存流并丢弃 | 服务器**入站**(下载)流量 |
| 🚀 服务端直发 `/api/send` | 服务器主动向目标发送 | 服务器**出站**(上传)流量（目标端记为下载） |

> 注意：部分云厂商只对**出站**流量计费，入站可能免费，具体看你的账单口径。

## 快速开始（Docker）

### 方式一：一行命令部署（推荐）

在装有 Docker 的服务器上，复制任意一行执行即可，脚本会通过交互对话框依次询问**端口、用户名、密码、Telegram Bot Token、Telegram 用户 ID**，然后自动构建并启动容器：

```bash
# curl 方式
curl -fsSL https://raw.githubusercontent.com/threce/traffic-burner/main/deploy.sh | bash

# wget 方式
wget -qO- https://raw.githubusercontent.com/threce/traffic-burner/main/deploy.sh | bash
```

脚本为**菜单式**：启动后显示 `[1]安装 [2]卸载 [3]更新 [0]退出`，依赖（git/curl/wget/docker/compose）缺失时自动安装，并自动 `git clone` 源码到 `~/traffic-burner-deploy`。访问 `http://<服务器IP>:<端口>`，用你设置的用户名/密码 + Telegram 验证码登录。

> 在 @BotFather 创建机器人后，把收到的 **Bot Token** 和你的 **Telegram 用户 ID**（访问 @userinfobot 获取）填入对话框，即可启用验证码登录与 TG 指令控制。

### 方式二：docker compose

```bash
cp .env.example .env      # 修改端口、用户名、密码
docker compose up -d --build
```

### 方式三：docker run 手动构建

```bash
docker build -t traffic-burner:latest .
docker run -d --name traffic-burner -p 8080:8080 \
  -e PORT=8080 -e AUTH_USER=admin -e AUTH_PASS=你的强密码 \
  -e TG_BOT_TOKEN=你的BotToken -e TG_CHAT_ID=你的用户ID \
  traffic-burner:latest
```

部署前请先在云厂商安全组/防火墙放行端口。

## 多台服务器互打流量（推荐玩法）

单台机器的出站+入站带宽往往不对称，且浏览器/自环会受路由限制。**多台服务器通过 HTTP 直发互相指向对方**，能同时用满每台的上传和下载流量，且**后台运行、无需浏览器常驻**：

1. 在每台服务器上都部署 `traffic-burner`（用脚本或 compose），并放行对应端口（记住每台的用户名/密码）。
2. 登录服务器 A，进入「🚀 服务端直发」，选择 **「HTTP 打到对方 /api/upload」**，目标填 **服务器 B 的地址**（如 `http://B公网IP:端口`），并在「对方用户名/密码」填 B 部署时设置的管理员账号。
3. 在服务器 B 也同样操作、指向 A。
4. 结果：
   - A 的直发向 B 发送数据 → **A 消耗「上传流量」**（出站），**B 消耗「下载流量」**（入站，被 `handleUpload` 丢弃）。
   - B 的直发向 A 发送数据 → **B 消耗「上传流量」**，**A 消耗「下载流量」**。
   - 双向对等，两台机器同时把上传和下载都跑满。

> 另一种「单机自环」用法：直发目标填**本机公网 IP + 本服务端口**（自环），数据出站又入站，一条连接同时消耗上传+下载。需安全组放行。

### 直发说明

- **裸 TCP 模式**（`target = host:port`、`mode=tcp`）：适合目标是一个能快速读走数据的 TCP 服务。
- **HTTP 模式**（`target = http://host:port`、`mode=http`）：最适合 `traffic-burner` 之间的互打，因为目标端的 `handleUpload` 会 `io.Copy(io.Discard, ...)` 收下并丢弃请求体，直接消耗目标机的「下载流量」。
- 直发任务由服务器后台执行，关闭页面也不中断；可通过「⏹ 停止」或 `POST /api/sendstop` 取消。

## Telegram 指令控制

绑定 TGBot 后，直接打开你的 Telegram 与机器人对话，即可远程查看和操控消耗：

| 指令 | 作用 |
|------|------|
| `/status` | 查看实时统计（上传/下载/连接/直发状态） |
| `/send <host:port> <threads> <seconds>` | 裸 TCP 直发到目标 |
| `/send <http://url> <threads> <seconds>` | HTTP 直发到对方 `/api/upload` |
| `/burn <up|down|both> <size> <host:port>` | 按方向启动直发（size 如 100MB/2GB） |
| `/stop` | 停止服务端直发 |
| `/reset` | 清零统计 |
| `/bind` | 绑定当前聊天为本机用户 |
| `/help` | 显示帮助 |

> 登录时，后端会生成一次性验证码并通过机器人推送给你，输入验证码 + 用户名 + 密码即可登录。

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `PORT` | `8080` | 容器内监听端口 |
| `AUTH_USER` | `admin` | 登录用户名 |
| `AUTH_PASS` | `changeme` | 登录密码 |
| `TG_BOT_TOKEN` | 无 | Telegram Bot Token（来自 @BotFather），启用验证码登录与 TG 指令 |
| `TG_CHAT_ID` | 无 | 你的 Telegram 用户 ID（来自 @userinfobot） |

## 目录结构

```
traffic-burner/
├── main.go        # 入口、路由、登录/会话鉴权
├── auth.go        # 登录验证码、session token
├── commands.go    # Telegram 指令控制
├── telegram.go    # Telegram Bot（getUpdates长轮询 + 验证码）
├── handler.go     # 下载/上传/服务端直发/统计 API
├── stats.go       # 全局统计与随机缓冲
├── web/
│   └── index.html # 单页前端（毛玻璃 UI）
├── Dockerfile
├── docker-compose.yml
├── .env.example
├── deploy.sh      # 菜单式部署脚本（安装/卸载/更新）
└── README.md
```

## 安全声明

- 请为所有部署设置强用户名/密码。
- 不要在公网无鉴权开放此服务，否则任何人可调用你的 API 消耗流量。
- 本工具仅用于消耗你**自己购买的剩余流量**，请遵守云厂商服务条款。
