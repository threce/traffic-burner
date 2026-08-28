#!/usr/bin/env bash
# =============================================================
#  traffic-burner 一键部署脚本
#
#  用法（在有 docker 和 docker compose 的服务器上）：
#     1) git clone https://github.com/threce/traffic-burner.git
#     2) cd traffic-burner
#     3) bash deploy.sh
#
#  脚本会通过交互对话框收集：端口、用户名、密码，然后生成密码文件
#  ${HOME}/.traffic-burner/.env，再用 docker compose 拉起容器。
# =============================================================
set -euo pipefail

# ---------- 检测 docker ----------
if ! command -v docker >/dev/null 2>&1; then
  echo "❌ 未检测到 docker，请先安装：curl -fsSL https://get.docker.com | sh"
  exit 1
fi
if ! docker compose version >/dev/null 2>&1 && ! command -v docker-compose >/dev/null 2>&1; then
  echo "❌ 未检测到 docker compose，请安装 docker compose 插件。"
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

# ---------- 生成 .env（含密码，不提交 git） ----------
env_dir="${HOME}/.traffic-burner"
mkdir -p "${env_dir}"
cat > "${env_dir}/.env" <<EOF
TB_PORT=${tb_port}
TB_USER=${tb_user}
TB_PASS=${tb_pass}
EOF
echo "✅ 配置已写入 ${env_dir}/.env（该文件包含密码，请勿提交到 git）"

# ---------- 用 docker compose 启动 ----------
COMPOSE_FILE="docker-compose.yml"
if [ -f "${COMPOSE_FILE}" ]; then
  echo "✅ 正在构建并启动容器…"
  if command -v docker-compose >/dev/null 2>&1 && ! docker compose version >/dev/null 2>&1; then
    docker-compose --env-file "${env_dir}/.env" up -d --build
  else
    docker compose --env-file "${env_dir}/.env" up -d --build
  fi
else
  echo "❌ 未找到 docker-compose.yml，请确认你在仓库根目录运行本脚本。"
  exit 1
fi

echo ""
echo "=============================================="
echo "   🎉 部署完成！"
echo "   访问地址:  http://<服务器IP>:${tb_port}"
echo "   用户名  :  ${tb_user}"
echo "   密码    :  ${tb_pass}"
echo ""
echo "   提示: 若端口未开通，请到云厂商控制台放行 TCP ${tb_port}。"
echo "=============================================="
