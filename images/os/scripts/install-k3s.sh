#!/bin/sh
set -euxo pipefail

# Taken from https://github.com/k3s-io/k3s/blob/bdb2a3e5/install.sh#L371-L383
K3S_VERSION=$(curl -w '%{url_effective}' -L -s -S https://update.k3s.io/v1-release/channels/stable -o /dev/null | sed -e 's|.*/||')
curl \
  -sfL "https://github.com/k3s-io/k3s/releases/download/${K3S_VERSION}/k3s" \
  -o /usr/local/bin/k3s;
chmod +x /usr/local/bin/k3s;

# Taken from https://github.com/k3s-io/k3s/blob/bdb2a3e5/install.sh#L780-797
for tool in kubectl crictl ctr; do
  ln -sf /usr/local/bin/k3s "/usr/local/bin/${tool}";
done

dnf \
  --repofrompath=k3s-selinux,https://rpm.rancher.io/k3s/stable/common/centos/9/noarch \
  --setopt=k3s-selinux.gpgcheck=1 \
  --setopt=k3s-selinux.gpgkey=https://rpm.rancher.io/public.key \
  --assumeyes \
  --disablerepo='*' \
  --enablerepo='k3s-selinux' \
  install k3s-selinux;

dnf clean all;
