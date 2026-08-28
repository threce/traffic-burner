#!/usr/bin/env bash
# =============================================================
#  traffic-burner 管理脚本（菜单式：安装 / 卸载 / 更新）
#
#  用法一（服务器上任意目录，一行命令）：
#     curl -fsSL https://raw.githubusercontent.com/threce/traffic-burner/main/deploy.sh | bash
#
#  用法二：
#     wget -qO- https://raw.githubusercontent.com/threce/traffic-burner/main/deploy.sh | bash
#
#  启动后显示菜单：
#     [1] 安装   [2] 卸载   [3] 更新   [0] 退出
# =============================================================
set -euo pipefail

REPO_URL="https://github.com/threce/traffic-burner.git"
WORK_DIR="${HOME}/traffic-burner-deploy"

# ---------- 依赖准备：检测缺失并自动安装 git / curl / wget / docker ----------
detect_pkgmgr() {
  if command -v apt-get >/dev/null 2>&1; then echo "apt"
  elif command -v dnf >/dev/null 2>&1; then echo "dnf"
  elif command -v yum >/dev/null 2>&1; then echo "yum"
  elif command -v apk >/dev/null 2>&1; then echo "apk"
  else echo ""; fi
}

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

# 检查并自动安装运行环境依赖；并检测 docker 权限，必要时自动用 sudo 执行
ensure_deps() {
  echo "📦 正在检查运行环境依赖…"
  local tool
  for tool in curl wget git; do
    if command -v "${tool}" >/dev/null 2>&1; then
      echo "  ✓ ${tool} 已安装"
    else
      echo "  ⬆ 未检测到 ${tool}，正在自动安装…"
      install_pkg "${tool}"
      echo "  ✓ ${tool} 安装完成"
    fi
  done

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

  if ! docker compose version >/dev/null 2>&1 && ! command -v docker-compose >/dev/null 2>&1; then
    echo "  ⬆ 未检测到 docker compose，正在自动安装…"
    if command -v curl >/dev/null 2>&1; then
      curl -fsSL "https://github.com/docker/compose/releases/latest/download/docker-compose-$(uname -s)-$(uname -m)" \
        -o /usr/local/bin/docker-compose
      chmod +x /usr/local/bin/docker-compose
      echo "  ✓ docker compose 安装完成"
    else
      echo "❌ 未检测到 docker compose，缺 curl 无法自动安装，请手动安装。"
      exit 1
    fi
  fi

  # ---- docker 权限自检：当前用户无法访问 docker daemon 时，尝试用 sudo ----
  if ! docker info >/dev/null 2>&1; then
    if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
      echo "  ⚠ 当前用户无 docker 权限，已自动切换为 sudo 执行 docker 命令。"
      DOCKER="sudo docker"
      COMPOSE="sudo docker compose"
      COMPOSE_V1="sudo docker-compose"
    else
      echo "  ❌ 当前用户无 docker 权限，且无法用 sudo 执行。"
      echo "     请执行下面命令把当前用户加入 docker 组后重新登录，再重跑本脚本："
      echo "       sudo usermod -aG docker \$USER && newgrp docker"
      echo "     或直接以 root 运行。"
      exit 1
    fi
  else
    DOCKER="docker"
    COMPOSE="docker compose"
    COMPOSE_V1="docker-compose"
  fi
  echo "===== 依赖检查完成 ====="
}

# 读取用户输入（从 /dev/tty，支持 curl|bash）
input() { read -r -p "$1" "$2" < /dev/tty; }
secret() { read -r -s -p "$1" "$2" < /dev/tty; }

# ---------- 安装 ----------
do_install() {
  ensure_deps

  echo ""
  echo "------------------------------------------------"
  echo "   🔥 开始安装 Traffic Burner"
  echo "------------------------------------------------"
  local default_port=8080 tb_port tb_user tb_pass tg_token tg_chat
  input "请输入对外端口 [默认 ${default_port}]: " tb_port
  tb_port="${tb_port:-${default_port}}"
  if ! [[ "${tb_port}" =~ ^[0-9]+$ ]] || [ "${tb_port}" -lt 1 ] || [ "${tb_port}" -gt 65535 ]; then
    echo "❌ 端口无效，使用默认 ${default_port}。"
    tb_port="${default_port}"
  fi

  input "请输入管理用户名 [默认 admin]: " tb_user
  tb_user="${tb_user:-admin}"

  secret "请输入管理密码（输入时不显示，建议 ≥12 位随机）[默认 changeme]: " tb_pass
  echo ""
  tb_pass="${tb_pass:-changeme}"

  echo ""
  echo "--- 绑定 Telegram Bot（用于动态验证码登录 + TG 指令控制）---"
  echo "    从 @BotFather 获取 Bot Token（形如 123456:ABC-DEF...）"
  input "请输入 Telegram Bot Token（留空则禁用 TGBot，仅账密登录）: " tg_token
  if [ -n "${tg_token}" ]; then
    echo "    你的个人用户 ID 可访问 @userinfobot 获取（形如 123456789）"
    input "请输入你的 Telegram 用户 ID (chat_id): " tg_chat
  fi
  tg_token="${tg_token:-}"
  tg_chat="${tg_chat:-}"

  echo ""
  echo "------------------------------------------------"
  echo "  将使用以下配置："
  echo "    端口   : ${tb_port}"
  echo "    用户名 : ${tb_user}"
  echo "    密码   : ${tb_pass}"
  if [ -n "${tg_token}" ]; then
    echo "    TGBot  : 已启用 (chat_id=${tg_chat})"
  else
    echo "    TGBot  : 未启用（仅账密登录）"
  fi
  echo "------------------------------------------------"

  echo "✅ 正在拉取仓库源码到 ${WORK_DIR} …"
  rm -rf "${WORK_DIR}"
  git clone --depth 1 "${REPO_URL}" "${WORK_DIR}"

  cat > "${WORK_DIR}/.env" <<EOF
TB_PORT=${tb_port}
TB_USER=${tb_user}
TB_PASS=${tb_pass}
TG_BOT_TOKEN=${tg_token}
TG_CHAT_ID=${tg_chat}
EOF

  echo "✅ 正在构建并启动容器…"
  (cd "${WORK_DIR}" && compose_up)

  echo ""
  echo "=============================================="
  echo "   🎉 安装完成！"
  echo "   访问地址:  http://<服务器IP>:${tb_port}"
  echo "   用户名  :  ${tb_user}"
  echo "   密码    :  ${tb_pass}"
  echo "   工作目录: ${WORK_DIR}"
  echo "=============================================="
}

# ---------- 卸载 ----------
do_uninstall() {
  echo ""
  echo "------------------------------------------------"
  echo "   🔥 卸载 Traffic Burner"
  echo "------------------------------------------------"
  if [ ! -d "${WORK_DIR}" ]; then
    echo "❌ 未找到部署目录 ${WORK_DIR}，可能尚未安装。"
    return 1
  fi
  echo "✅ 正在停止并移除容器…"
  (cd "${WORK_DIR}" && compose_down 2>/dev/null || true)
  echo "✅ 正在删除镜像…"
  ${DOCKER} rmi traffic-burner:latest 2>/dev/null || true
  echo "✅ 正在清理工作目录…"
  rm -rf "${WORK_DIR}"
  echo "   ✅ 卸载完成，已删除容器、镜像与 ${WORK_DIR}。"
}

# ---------- 更新 ----------
do_update() {
  ensure_deps
  if [ ! -d "${WORK_DIR}" ]; then
    echo "❌ 未找到部署目录 ${WORK_DIR}，可能尚未安装，请先选择[1]安装。"
    return 1
  fi
  echo "✅ 正在拉取最新源码…"
  (cd "${WORK_DIR}" && git pull --ff-only)
  echo "✅ 正在重新构建并重启容器…"
  (cd "${WORK_DIR}" && compose_up)
  echo "   ✅ 更新完成。"
}

# ---------- docker compose 封装 ----------
compose_up() {
  if command -v docker-compose >/dev/null 2>&1 && ! docker compose version >/dev/null 2>&1; then
    ${COMPOSE_V1} --env-file "${WORK_DIR}/.env" up -d --build
  else
    ${COMPOSE} --env-file "${WORK_DIR}/.env" up -d --build
  fi
}
compose_down() {
  if command -v docker-compose >/dev/null 2>&1 && ! docker compose version >/dev/null 2>&1; then
    ${COMPOSE_V1} --env-file "${WORK_DIR}/.env" down
  else
    ${COMPOSE} --env-file "${WORK_DIR}/.env" down
  fi
}

# ---------- 主菜单 ----------
main() {
  while true; do
    echo ""
    echo "=============================================="
    echo "   🔥 Traffic Burner 管理菜单"
    echo "=============================================="
    echo "   [1] 安装"
    echo "   [2] 卸载"
    echo "   [3] 更新"
    echo "   [0] 退出"
    echo "=============================================="
    local choice
    input "请选择 [0-3]: " choice
    case "${choice:-0}" in
      1) do_install ;;
      2) do_uninstall ;;
      3) do_update ;;
      0) echo "再见！"; exit 0 ;;
      *) echo "❌ 无效选项，请输入 0-3。";;
    esac
  done
}

main
