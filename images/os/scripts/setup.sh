#!/bin/sh
set -euxo pipefail

mkdir -p /var/opt;
rm -rf /opt;
ln -s var/opt /opt;

systemctl enable var-home.mount var-mnt-data.mount k3s.service;

systemctl set-default multi-user.target;
