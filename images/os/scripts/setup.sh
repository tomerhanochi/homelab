#!/bin/sh
set -euxo pipefail

mkdir -p /var/opt;
rm -rf /opt;
ln -s var/opt /opt;

systemctl enable \
  var-home.mount \
  var-mnt-data-media-books.mount \
  var-mnt-data-media-movies.mount \
  var-mnt-data-media-music.mount \
  var-mnt-data-media-shows.mount \
  var-mnt-data-torrents-movies.mount \
  var-mnt-data-torrents-music.mount \
  var-mnt-data-torrents-shows.mount \
  var-mnt-wd-005t-01.mount \
  var-mnt-wd-005t-02.mount \
  k3s.service;

systemctl set-default multi-user.target;
