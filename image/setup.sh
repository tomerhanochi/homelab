#!/usr/bin/env sh
set -euxo pipefail;

###############
#---- K3S ----#
###############
# Taken from https://github.com/k3s-io/k3s/blob/master/install.sh#L371
K3S_VERSION=$(curl -w '%{url_effective}' -L -s -S https://update.k3s.io/v1-release/channels/stable -o /dev/null | sed -e 's|.*/||');
curl \
  -sfL "https://github.com/k3s-io/k3s/releases/download/${K3S_VERSION}/k3s" \
  -o /usr/local/bin/k3s;
chmod +x /usr/local/bin/k3s;

# Taken from https://github.com/k3s-io/k3s/blob/master/install.sh#L757
for tool in kubectl crictl ctr; do
  ln -sf /usr/local/bin/k3s "/usr/local/bin/${tool}";
done

####################
#---- PACKAGES ----#
####################
dnf \
  --repofrompath=k3s-selinux,https://rpm.rancher.io/k3s/stable/common/centos/9/noarch \
  --setopt=k3s-selinux.gpgcheck=1 \
  --setopt=k3s-selinux.gpgkey=https://rpm.rancher.io/public.key \
  --assumeyes \
  --disablerepo='*' \
  --enablerepo='fedora,updates,k3s-selinux' \
  install audit firewalld k3s-selinux NetworkManager openssh-server polkit;

####################
#---- FIREWALL ----#
####################
# Enable access to all control plane components
firewall-offline-cmd --add-service=kube-control-plane;
# Enable internal communication between pods
firewall-offline-cmd --zone=trusted --add-source=10.42.0.0/16;
# Enable internal communication between services
firewall-offline-cmd --zone=trusted --add-source=10.43.0.0/16;

###################
#---- SYSTEMD ----#
###################
systemctl enable var-home.mount k3s.service
