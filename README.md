# openstack-docker-driver

Generic Openstack Docker volume plugin.  This is based off of https://github.com/j-griffith/cinder-docker-driver with a lot of the same code but focused on only providing simple cinder mapping to nova-compute hypervisors without the iscsi bits.  Currently only tested with docker 1.12.6 on CentOS7.

## Install

`curl -sSl https://raw.githubusercontent.com/mvollman/openstack-docker-driver/master/install.sh | sudo bash`

## Build from source

### Build it!
```
git clone https://github.com/mvollman/openstack-docker-driver
cd openstack-docker-driver
export GOPATH=$PWD
go get ./...
go build
```

### Run it!

Source an openstack RC file like you would to run Openstack CLI commands and run
```
source openrc
./openstack-docker-driver
```


## Configure

Edit /etc/sysconfig/openstack-docker-driver or /etc/default/openstack-docker-driver and setup your Openstack authentication

Set the options below:
```
OS_REGION_NAME=
OS_USERNAME=
OS_PASSWORD=
OS_AUTH_URL=
OS_PROJECT_NAME=
OS_TENANT_NAME=
OS_TENANT_ID=
```

Then start the driver:

`sudo service openstack-docker-driver start`

And restart docker:

`sudo service docker restart`

## Usage

### Create

Example 10GB ceph cinder volume:

`docker volume create -d openstack --name myvolume -o type=ceph -o size=10`

### Run

Example mount your volume to the a container:

`docker run -v myvolume:/Data --volume-driver=openstack -i -t ubuntu /bin/bash`

## TODO

The driver works on the happy path.  What's left?

- Test all the edge cases
- More logging and debug messages
- Remove code duplication
- Upstart service script
- Per volume file system types


