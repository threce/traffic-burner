#!/usr/bin/env bash
# =============================================================
#  traffic-burner 一键部署脚本（自包含，可 curl | bash 一行执行）
#
#  用法一（在你自己的服务器上，任意目录）：
#     curl -fsSL https://raw.githubusercontent.com/threce/traffic-burner/main/deploy.sh | bash
#
#  用法二：
#     wget -qO- https://raw.githubusercontent.com/threce/traffic-burner/main/deploy.sh | bash
#
#  脚本会：
#    1) 检测依赖 git/curl/wget/docker，缺失自动安装
#    2) 交互收集：端口、用户名、密码
#    3) git clone 仓库到 ${HOME}/traffic-burner-deploy
#    4) 用 docker build + docker compose 拉起容器
# =============================================================
set -euo pipefail

REPO_URL="https://github.com/threce/traffic-burner.git"
WORK_DIR="${HOME}/traffic-burner-deploy"

# ---------- 依赖准备：检测缺失并自动安装 git / curl / wget / docker ----------
# 返回系统包管理器名：apt/yum/dnf/apk，找不到返回空
detect_pkgmgr() {
  if command -v apt-get >/dev/null 2>&1; then echo "apt"
  elif command -v dnf >/dev/null 2>&1; then echo "dnf"
  elif command -v yum >/dev/null 2>&1; then echo "yum"
  elif command -v apk >/dev/null 2>&1; then echo "apk"
  else echo ""; fi
}

# 用包管理器安装一个或多个包
install_pkg() {
  local pm; pm="$(detect_pkgmgr)"
  if [ -z "${pm}" ]; then
    echo "❌ 无法识别系统包管理器（apt/yum/dnf/apk），请手动安装依赖。"
    exit 1
  fi
  case "${pm}" in
    apt) apt-get update -qq && apt-get install -y -qq "$@";;
    dnf) dnf install -y "$@";;
    yum) yum install -y "$@";;
    apk) apk --no-cache add "$@";;
  esac
}

echo "=============================================="
echo "   🔥 Traffic Burner 一键部署"
echo "=============================================="
echo "📦 正在检查运行环境依赖…"

# ---- 基础工具：curl / wget / git ----
for tool in curl wget git; do
  if command -v "${tool}" >/dev/null 2>&1; then
    echo "  ✓ ${tool} 已安装"
  else
    echo "  ⬆ 未检测到 ${tool}，正在自动安装…"
    install_pkg "${tool}"
    echo "  ✓ ${tool} 安装完成"
  fi
done

# ---- Docker ----
if ! command -v docker >/dev/null 2>&1; then
  echo "  ⬆ 未检测到 docker，正在自动安装（官方脚本）…"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL https://get.docker.com | sh
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- https://get.docker.com | sh
  else
    echo "❌ 安装 docker 需要 curl 或 wget。"
    exit 1
  fi
  echo "  ✓ docker 安装完成"
else
  echo "  ✓ docker 已安装"
fi

# ---- Docker Compose 插件（docker compose / docker-compose）----
if ! docker compose version >/dev/null 2>&1 && ! command -v docker-compose >/dev/null 2>&1; then
  echo "  ⬆ 未检测到 docker compose，正在自动安装（docker compose 插件）…"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "https://github.com/docker/compose/releases/latest/download/docker-compose-$(uname -s)-$(uname -m)" \
      -o /usr/local/bin/docker-compose
    chmod +x /usr/local/bin/docker-compose
    echo "  ✓ docker compose 安装完成"
  else
    echo "❌ 未检测到 docker compose，缺 curl 无法自动安装，请手动安装 docker compose 插件。"
    exit 1
  fi
fi

echo "===== 依赖检查完成 ====="

# ---------- 对话框：端口 ----------
default_port=8080
read -r -p "请输入对外端口 [默认 ${default_port}]: " tb_port < /dev/tty
tb_port="${tb_port:-${default_port}}"
# 校验端口是数字
if ! [[ "${tb_port}" =~ ^[0-9]+$ ]] || [ "${tb_port}" -lt 1 ] || [ "${tb_port}" -gt 65535 ]; then
  echo "❌ 端口无效，使用默认 ${default_port}。"
  tb_port="${default_port}"
fi

# ---------- 对话框：用户名 ----------
default_user=admin
read -r -p "请输入管理用户名 [默认 ${default_user}]: " tb_user < /dev/tty
tb_user="${tb_user:-${default_user}}"

# ---------- 对话框：密码 ----------
default_pass=changeme
read -r -s -p "请输入管理密码（输入时不显示，建议 ≥12 位随机） [默认 ${default_pass}]: " tb_pass < /dev/tty
echo ""
tb_pass="${tb_pass:-${default_pass}}"

echo ""
echo "------------------------------------------------"
echo "  将使用以下配置："
echo "    端口   : ${tb_port}"
echo "    用户名 : ${tb_user}"
echo "    密码   : ${tb_pass}"              # 仅本次终端显示
echo "------------------------------------------------"

# ---------- 拉取仓库源码 ----------
echo "✅ 正在拉取仓库源码到 ${WORK_DIR} …"
rm -rf "${WORK_DIR}"
git clone --depth 1 "${REPO_URL}" "${WORK_DIR}"

# ---------- 写入 .env（含密码，不提交 git） ----------
cat > "${WORK_DIR}/.env" <<EOF
TB_PORT=${tb_port}
TB_USER=${tb_user}
TB_PASS=${tb_pass}
EOF

echo "✅ 正在构建并启动容器…"
# ---------- docker compose 启动 ----------
cd "${WORK_DIR}"
if command -v docker-compose >/dev/null 2>&1 && ! docker compose version >/dev/null 2>&1; then
  docker-compose --env-file "${WORK_DIR}/.env" up -d --build
else
  docker compose --env-file "${WORK_DIR}/.env" up -d --build
fi

echo ""
echo "=============================================="
echo "   🎉 部署完成！"
echo "   访问地址:  http://<服务器IP>:${tb_port}"
echo "   用户名  :  ${tb_user}"
echo "   密码    :  ${tb_pass}"
echo ""
echo "   提示: 若端口未开通，请到云厂商控制台放行 TCP ${tb_port}。"
echo "   工作目录: ${WORK_DIR}（日志可用 docker logs traffic-burner 查看）"
echo "=============================================="
