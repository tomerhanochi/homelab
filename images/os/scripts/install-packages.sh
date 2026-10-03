#!/bin/sh
set -euxo pipefail

dnf \
  --assumeyes \
  install audit NetworkManager openssh-server polkit helix;

dnf clean all;
