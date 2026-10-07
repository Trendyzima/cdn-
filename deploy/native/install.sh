#!/usr/bin/env bash
set -euo pipefail

ROOT=/opt/testagram-edge
BIN="$ROOT/testagram-edge"
CONF=/etc/testagram-edge
CACHE=/var/lib/testagram-edge/cache

install -d -m 0755 "$ROOT" "$CONF" "$CACHE"
if ! id testagram-edge >/dev/null 2>&1; then
  useradd --system --home "$ROOT" --shell /usr/sbin/nologin testagram-edge
fi
chown -R testagram-edge:testagram-edge "$ROOT" "$CACHE"

install -m 0644 deploy/native/testagram-edge.service /etc/systemd/system/testagram-edge.service
if [ ! -f "$CONF/edge.env" ]; then
  install -m 0600 deploy/native/edge.env.example "$CONF/edge.env"
  chown testagram-edge:testagram-edge "$CONF/edge.env"
  echo "Edit $CONF/edge.env, install the compiled testagram-edge binary at $BIN, then run:"
  echo "  systemctl daemon-reload && systemctl enable --now testagram-edge"
  exit 0
fi

systemctl daemon-reload
systemctl enable --now testagram-edge
