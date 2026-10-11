#!/bin/sh
set -euxo pipefail

mkdir -p /var/opt;
rm -rf /opt;
ln -s var/opt /opt;

systemctl enable \
  var-home.mount \
  var-mnt-data-books.mount \
  var-mnt-data-movies.mount \
  var-mnt-data-music.mount \
  var-mnt-data-shows.mount \
  var-mnt-wd-005t-01.mount \
  var-mnt-wd-005t-02.mount \
  k3s.service;

systemctl set-default multi-user.target;
