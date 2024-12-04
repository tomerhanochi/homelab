FROM quay.io/fedora/fedora-bootc:41

COPY /usr/lib/ostree /usr/lib/ostree
# This rebuilds the initramfs to include changes made to the ostree-prepare-root config.
RUN kver=$(ls /usr/lib/modules); dracut -vf /usr/lib/modules/$kver/initramfs.img $kver

COPY /usr/share/authselect/default/systemd /usr/share/authselect/default/systemd
RUN authselect select systemd

COPY /usr/lib/systemd/system /usr/lib/systemd/system
RUN systemctl enable var-home.mount

COPY /usr/lib/userdb /usr/lib/userdb

COPY /usr/share/factory /usr/share/factory
COPY /usr/lib/tmpfiles.d /usr/lib/tmpfiles.d
