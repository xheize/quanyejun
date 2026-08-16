#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo "Run this installer as root inside the LXC guest." >&2
  exit 1
fi

if [[ $# -ne 1 || ! -f $1 ]]; then
  echo "Usage: sudo ./deploy/lxc/install.sh /path/to/quanyejun-runtime" >&2
  exit 1
fi

binary_path=$(readlink -f "$1")
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
common_dir=$(cd -- "${script_dir}/../common" && pwd)

if ! id quanyejun >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/quanyejun/home --create-home --shell /usr/sbin/nologin quanyejun
fi

install -d -o quanyejun -g quanyejun -m 0750 \
  /var/lib/quanyejun/home \
  /var/lib/quanyejun/workspace \
  /var/cache/quanyejun
install -d -o root -g quanyejun -m 0750 /etc/quanyejun
install -o root -g root -m 0755 "${binary_path}" /usr/local/bin/quanyejun-runtime
install -o root -g root -m 0644 "${script_dir}/quanyejun.service" /etc/systemd/system/quanyejun.service

if [[ ! -f /etc/quanyejun/config.env ]]; then
  install -o root -g quanyejun -m 0640 "${common_dir}/quanyejun.env.example" /etc/quanyejun/config.env
fi

systemctl daemon-reload
systemctl enable --now quanyejun.service
systemctl --no-pager --full status quanyejun.service
