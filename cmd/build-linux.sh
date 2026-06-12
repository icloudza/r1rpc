#!/usr/bin/env bash
#
# 一键打包 r1rpc 的 Linux 发布包。
#
#   流程：构建前端(embed) → go vet → 去符号静态交叉编译 → 打 tar.gz（二进制 + 配置模板 + systemd 单元）
#   产物：dist/r1rpc-linux-<arch>  和  dist/r1rpc-linux-<arch>.tar.gz
#
# 用法：
#   cmd/build-linux.sh                 # 默认 amd64（云服务器绝大多数）
#   cmd/build-linux.sh arm64           # ARM（鲲鹏 / Graviton）
#   cmd/build-linux.sh --skip-web      # 跳过前端构建（用已 embed 的 ui）
#
set -euo pipefail

# 切到仓库根目录（本脚本位于 cmd/ 下）
cd "$(dirname "$0")/.."
ROOT="$(pwd)"

ARCH="amd64"
SKIP_WEB=0
for arg in "$@"; do
  case "$arg" in
    amd64|arm64)  ARCH="$arg" ;;
    --arch=*)     ARCH="${arg#*=}" ;;
    --skip-web)   SKIP_WEB=1 ;;
    -h|--help)    sed -n '2,16p' "$0"; exit 0 ;;
    *) echo "未知参数: ${arg}（用法见 --help）" >&2; exit 1 ;;
  esac
done
case "$ARCH" in amd64|arm64) ;; *) echo "不支持的架构: ${ARCH}（仅 amd64 / arm64）" >&2; exit 1 ;; esac

OUT="$ROOT/dist"
BIN="r1rpc-linux-$ARCH"
TARBALL="$BIN.tar.gz"
mkdir -p "$OUT"

echo "==> [1/4] go vet（静态检查，失败即中止）"
go vet ./...

echo "==> [2/4] 构建前端（React 面板 go:embed 进二进制）"
if [[ "$SKIP_WEB" -eq 1 ]]; then
  echo "    跳过（--skip-web，使用已 embed 的 internal/web/ui）"
elif [[ -d "$ROOT/web" ]] && command -v npm >/dev/null 2>&1; then
  ( cd "$ROOT/web" && npm run build )
else
  echo "    跳过：未找到 web 目录或 npm（如需最新面板请装 Node 后重跑）"
fi

echo "==> [3/4] 交叉编译 linux/${ARCH}（CGO=0 纯静态 · 去符号 -s -w · -trimpath 抹路径）"
GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w" -o "$OUT/$BIN" ./cmd/server
echo "    $(file "$OUT/$BIN" | sed "s#$OUT/##")"
echo "    大小: $(du -h "$OUT/$BIN" | cut -f1)"

echo "==> [4/4] 打包 ${TARBALL}（二进制 + 配置模板 + systemd 单元）"
# 配置模板
[[ -f "$ROOT/config.example.yaml" ]] && cp "$ROOT/config.example.yaml" "$OUT/config.yaml"
# systemd 单元（ExecStart 对应本次架构的二进制名）
cat > "$OUT/r1rpc.service" <<EOF
[Unit]
Description=r1rpc - 真机 RPC 中继
After=network.target mysql.service

[Service]
Type=simple
WorkingDirectory=/opt/r1rpc
ExecStart=/opt/r1rpc/$BIN
Restart=always
RestartSec=3
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

FILES=("$BIN" "r1rpc.service")
[[ -f "$OUT/config.yaml" ]] && FILES+=("config.yaml")
tar -C "$OUT" -czf "$OUT/$TARBALL" "${FILES[@]}"

echo ""
echo "✅ 完成：dist/${TARBALL}（$(du -h "$OUT/$TARBALL" | cut -f1)）"
echo "   部署：scp dist/$TARBALL 服务器:/tmp/ && tar -xzf /tmp/$TARBALL -C /opt/r1rpc"
echo "        改 config.yaml → cp r1rpc.service /etc/systemd/system/ → systemctl enable --now r1rpc"
echo ""
echo "   注：-s -w 去掉了符号表与调试信息；Go 的 pclntab 仍含函数名（运行时栈回溯所需），"
echo "       如需进一步混淆函数名可改用 garble，详见之前说明。"
