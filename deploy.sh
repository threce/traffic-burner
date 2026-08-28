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
#    1) 交互收集：端口、用户名、密码
#    2) git clone 仓库到 ${HOME}/traffic-burner-deploy
#    3) 用 docker build + docker compose 拉起容器
# =============================================================
set -euo pipefail

REPO_URL="https://github.com/threce/traffic-burner.git"
WORK_DIR="${HOME}/traffic-burner-deploy"

# ---------- 检测 docker ----------
if ! command -v docker >/dev/null 2>&1; then
  echo "❌ 未检测到 docker，请先安装：curl -fsSL https://get.docker.com | sh"
  exit 1
fi
if ! docker compose version >/dev/null 2>&1 && ! command -v docker-compose >/dev/null 2>&1; then
  echo "❌ 未检测到 docker compose，请安装 docker compose 插件。"
  exit 1
fi
# ---------- 检测 git ----------
if ! command -v git >/dev/null 2>&1; then
  echo "❌ 未检测到 git，请先安装 git：apt install -y git / yum install -y git"
  exit 1
fi

echo "=============================================="
echo "   🔥 Traffic Burner 一键部署"
echo "=============================================="

# ---------- 对话框：端口 ----------
default_port=8080
read -r -p "请输入对外端口 [默认 ${default_port}]: " tb_port
tb_port="${tb_port:-${default_port}}"
# 校验端口是数字
if ! [[ "${tb_port}" =~ ^[0-9]+$ ]] || [ "${tb_port}" -lt 1 ] || [ "${tb_port}" -gt 65535 ]; then
  echo "❌ 端口无效，使用默认 ${default_port}。"
  tb_port="${default_port}"
fi

# ---------- 对话框：用户名 ----------
default_user=admin
read -r -p "请输入管理用户名 [默认 ${default_user}]: " tb_user
tb_user="${tb_user:-${default_user}}"

# ---------- 对话框：密码 ----------
default_pass=changeme
read -r -s -p "请输入管理密码（输入时不显示，建议 ≥12 位随机） [默认 ${default_pass}]: " tb_pass
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
