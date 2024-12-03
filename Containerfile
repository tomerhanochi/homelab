FROM quay.io/fedora/fedora-bootc:41

RUN dnf install --assumeyes bat eza fzf git ripgrep zoxide zsh && dnf remove --assumeyes qemu-* sssd-* && dnf clean all

COPY /usr /usr

RUN systemctl enable var-home.mount
RUN authselect select systemd
