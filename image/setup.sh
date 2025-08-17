#!/usr/bin/env sh
set -euxo pipefail;

###############
#---- K3S ----#
###############
# k3s is a multicall binary, and can function as multiple tools depending on the name its called with (similar to busybox).
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
  install polkit NetworkManager openssh-server firewalld k3s-selinux;

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
