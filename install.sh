#!/bin/sh
set -e

BIN_NAME=openstack-docker-driver
DRIVER_URL="https://github.com/mvollman/openstack-docker-driver/releases/download/v0.1.0/openstack-docker-driver"
BIN_DIR="/usr/bin"

do_upstart() {
cat <<EOFUPSTART >/etc/init/${BIN_NAME}.conf
### START SCRIPT ###
# vim:set ft=upstart ts=2 et:

description "${BIN_NAME}"
author "Mike Vollman <mvollman@cas.org>"

start on (runlevel [2345] and started udev and started rsyslog and local-filesystems)
stop on (runlevel [016] and udev and rsyslog and local-filesystems)

expect daemon
respawn
respawn limit 10 5

# environment variables
env RUNBIN="${BIN_DIR}/${BIN_NAME}"
env NAME="$BIN_NAME"
env DAEMON_OPTS=''

# Run the daemon
script
  set -a
  [ -f /etc/default/$BIN_NAME ] && . /etc/default/$BIN_NAME
  exec \$RUNBIN \$DAEMON_OPTS &
end script
### END SCRIPT ###
EOFUPSTART

}

do_systemd() {
cat <<EOFSYSD >/etc/systemd/system/${BIN_NAME}.service
[Unit]
Description=\"Openstack Docker Plugin daemon\"
Before=docker.service
Requires=${BIN_NAME}.service

[Service]
EnvironmentFile=-/etc/sysconfig/$BIN_NAME
TimeoutStartSec=0
ExecStart=$BIN_DIR/$BIN_NAME &

[Install]
WantedBy=docker.service
EOFSYSD

chmod 644 /etc/systemd/system/${BIN_NAME}.service
systemctl daemon-reload
systemctl enable $BIN_NAME

}

do_install() {
rm $BIN_DIR/$BIN_NAME 2>/dev/null|| true
curl -sSL -o $BIN_DIR/$BIN_NAME $DRIVER_URL
chmod +x $BIN_DIR/$BIN_NAME
osid=$(awk -F'=' '/^ID=/{print $2}' /etc/os-release)
osver=$(awk -F'=' '/^VERSION_ID=/{print $2}' /etc/os-release)
inittype='systemd'
config_path="/etc/sysconfig/$BIN_NAME"
if [[ "$osid" == 'ubuntu' ]] ; then
     config_path="/etc/default/$BIN_NAME"
     if [[ "$osver" == '"14.04"' ]]; then
       inittype='upstart'
     fi
fi

if [ ! -f $config_path ] ; then
cat << EOFENV > $config_path
# Driver Options
#MountPoint=/var/lib/$BIN_NAME
#FSType=ext4

# Openstack Authentication
OS_REGION_NAME=
OS_USERNAME=
OS_PASSWORD=
OS_AUTH_URL=
OS_PROJECT_NAME=
OS_TENANT_NAME=
OS_TENANT_ID=

EOFENV
fi

case $inittype in
'upstart') do_upstart ;;
'systemd') do_systemd ;;
*)  echo >&2 "Unknown OS version $osid $osver"
    exit 1
;;
esac

}

do_install
