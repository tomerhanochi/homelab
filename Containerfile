FROM quay.io/fedora/fedora-bootc:41

COPY /usr /usr

RUN systemctl enable var-home.mount
RUN authselect select systemd
