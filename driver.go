package main

import (
	"errors"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/docker/go-plugins-helpers/volume"
	"github.com/rackspace/gophercloud"
	"github.com/rackspace/gophercloud/openstack"
	"github.com/rackspace/gophercloud/openstack/blockstorage/v2/volumes"
	"github.com/rackspace/gophercloud/openstack/compute/v2/extensions/volumeattach"
	"github.com/rackspace/gophercloud/openstack/compute/v2/servers"
	"github.com/rackspace/gophercloud/pagination"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type OpenstackDriver struct {
	Client     *gophercloud.ProviderClient
	Mutex      *sync.Mutex
	MountPoint string
	FSType     string
}

func New() OpenstackDriver {
	fsType := os.Getenv("FSType")
	if fsType == "" {
		fsType = "ext4"
	}
	mountPoint := os.Getenv("MountPoint")
	if mountPoint == "" {
		mountPoint = "/var/lib/openstack-docker-driver"
	}
	_, err := os.Lstat(mountPoint)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(mountPoint, 0755); err != nil {
			log.Fatal("Failed to create Mount directory during driver init: %v", err)
		}
	}
	opts, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		log.Fatal("Missing authentication environment variables: ", err)
	}
	provider, err := openstack.AuthenticatedClient(opts)
	if err != nil {
		log.Fatal("Authentication failed: ", err)
	}
	d := OpenstackDriver{
		Mutex:      &sync.Mutex{},
		Client:     provider,
		MountPoint: mountPoint,
		FSType:     fsType,
	}
	return d
}

func (d OpenstackDriver) Capabilities(r volume.Request) volume.Response {
	return volume.Response{Capabilities: volume.Capability{Scope: "global"}}
}

func (d OpenstackDriver) parseOpts(r volume.Request) volumes.CreateOpts {
	opts := volumes.CreateOpts{}
	for k, v := range r.Options {
		switch k {
		case "size":
			vSize, err := strconv.Atoi(v)
			if err == nil {
				opts.Size = vSize
			}
		case "type":
			if r.Options["type"] != "" {
				opts.VolumeType = v
			}
		}
	}
	return opts
}

func (d OpenstackDriver) getInstanceUUID(name string) (string, error) {
	log.Info("Searching for instance ", name)
	compClient, err := openstack.NewComputeV2(d.Client, gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	})
	opts := servers.ListOpts{Name: name}
	pager := servers.List(compClient, opts)
	var myid string
	err = pager.EachPage(func(page pagination.Page) (bool, error) {
		serverList, err := servers.ExtractServers(page)
		if err != nil {
			log.Errorf("Get Instance Error: %s", err)
			return false, err
		}

		if len(serverList) > 1 {
			log.Error("Multiple instances with same name " + name)
			return false, errors.New("Multiple instances with same name " + name)
		}

		for _, s := range serverList {
			if s.Name == name {
				myid = s.ID
				return true, nil
			}
		}
		log.Error("Instance Not Found!")
		return false, errors.New("Instance Not Found")
	})
	if err != nil {
		log.Errorf("Extract Instance Error: %s", err)
		return myid, err
	}

	return myid, nil

}

func (d OpenstackDriver) getVolByName(name string) (volumes.Volume, error) {
	log.Info("Searching for volume ", name)
	endpointOpts := gophercloud.EndpointOpts{Region: os.Getenv("OS_REGION_NAME")}
	blockClient, err := openstack.NewBlockStorageV2(d.Client, endpointOpts)
	opts := volumes.ListOpts{Name: name}
	vols := volumes.List(blockClient, opts)
	var vol volumes.Volume
	err = vols.EachPage(func(page pagination.Page) (bool, error) {
		vList, err := volumes.ExtractVolumes(page)
		if err != nil {
			log.Errorf("Get Volume Error: %s", err)
			return false, err
		}

		if len(vList) > 1 {
			log.Error("Multiple volumes with same name " + name)
			return false, errors.New("Multiple volumes with same name " + name)
		}
		for _, v := range vList {
			log.Debugf("querying volume: %+v With name: %+v\n", v.ID, v.Name)
			if v.Name == name {
				vol = v
				return true, nil
			}
		}
		log.Error("Volume Not Found!")
		return false, errors.New("Volume Not Found")
	})
	if err != nil {
		log.Errorf("Extract Volume Error: %s", err)
		return volumes.Volume{}, err
	}

	return vol, nil
}

func (d OpenstackDriver) Create(r volume.Request) volume.Response {
	log.Infof("Create volume %s on %s", r.Name, "Cinder")
	d.Mutex.Lock()
	defer d.Mutex.Unlock()

	vol, err := d.getVolByName(r.Name)
	if err != nil {
		log.Errorf("Error getting existing Volume by Name: (volume %s, error %s)", vol, err.Error())
		return volume.Response{Err: err.Error()}
	}
	if vol.Name != "" {
		log.Errorf("Volume already exists: (volume: %s, id %s)", vol.Name, vol.ID)
		return volume.Response{Err: "Volume already exists with name: " + vol.Name + " and ID: " + vol.ID}
	}

	opts := d.parseOpts(r)
	opts.Name = r.Name
	log.Debugf("Creating with options: %+v", opts)
	endpointOpts := gophercloud.EndpointOpts{Region: os.Getenv("OS_REGION_NAME")}
	blockClient, err := openstack.NewBlockStorageV2(d.Client, endpointOpts)
	_, err = volumes.Create(blockClient, opts).Extract()
	if err != nil {
		log.Errorf("Failed to Create volume: %s\nEncountered error: %s", r.Name, err)
		return volume.Response{Err: err.Error()}
	}
	path := filepath.Join(d.MountPoint, r.Name)
	if err := os.Mkdir(path, os.ModeDir); err != nil {
		log.Errorf("Failed to create Mount directory: %v", err)
		return volume.Response{Err: err.Error()}
	}
	return volume.Response{}
}

func (d OpenstackDriver) Get(r volume.Request) volume.Response {
	log.Info("Get volume: ", r.Name)
	vol, err := d.getVolByName(r.Name)
	if err != nil {
		log.Errorf("Failed to retrieve volume `%s`: %s", r.Name, err.Error())
		return volume.Response{Err: err.Error()}
	}
	if vol.ID == "" {
		log.Errorf("Failed to retrieve volume named: %s", r.Name)
		err = errors.New("Volume Not Found")
		return volume.Response{Err: err.Error()}
	}
	path := filepath.Join(d.MountPoint, r.Name)
	return volume.Response{Volume: &volume.Volume{Name: r.Name, Mountpoint: path}}
}

func (d OpenstackDriver) List(r volume.Request) volume.Response {
	log.Info("Listing volumes")
	var vols []*volume.Volume
	endpointOpts := gophercloud.EndpointOpts{Region: os.Getenv("OS_REGION_NAME")}
	blockClient, err := openstack.NewBlockStorageV2(d.Client, endpointOpts)
	if err != nil {
		log.Fatal("Error initiating gophercloud cinder blockClient: ", err)
	}
	pager := volumes.List(blockClient, volumes.ListOpts{})
	err = pager.EachPage(func(page pagination.Page) (bool, error) {
		vList, _ := volumes.ExtractVolumes(page)

		for _, v := range vList {
			vols = append(vols, &volume.Volume{Name: v.Name})
		}
		return true, nil
	})
	return volume.Response{Volumes: vols}
}

func (d OpenstackDriver) Remove(r volume.Request) volume.Response {
	log.Info("Remove/Delete Volume: ", r.Name)
	vol, err := d.getVolByName(r.Name)
	log.Debugf("Remove/Delete Volume ID: %s", vol.ID)
	if err != nil {
		log.Errorf("Failed to retrieve volume named: ", r.Name, "during Remove operation", err)
		return volume.Response{Err: err.Error()}
	}
	endpointOpts := gophercloud.EndpointOpts{Region: os.Getenv("OS_REGION_NAME")}
	blockClient, err := openstack.NewBlockStorageV2(d.Client, endpointOpts)
	errRes := volumes.Delete(blockClient, vol.ID)
	log.Debugf("Response from Delete: %+v\n", errRes)
	if errRes.Err != nil {
		log.Errorf("Failed to Delete volume: %s\nEncountered error: %s", vol, errRes)
		log.Debugf("Error message: %s", errRes.ExtractErr())
		return volume.Response{Err: fmt.Sprintf("%s", errRes.ExtractErr())}
	}
	path := filepath.Join(d.MountPoint, r.Name)
	out, err := exec.Command("rmdir", path).CombinedOutput()
	if err != nil {
		log.Warningf("Remove dir call returned error: %s (%s)", err, out)
		if strings.Contains(string(out), "No such file or directory") {
			log.Debug("Ignore request for rmdir on missing directory")
		} else {
			log.Error("Failed to remove Mount directory: %v", err)
			return volume.Response{Err: err.Error()}
		}
	}
	return volume.Response{}
}

func (d OpenstackDriver) Path(r volume.Request) volume.Response {
	log.Info("Retrieve path info for volume: `", r.Name, "`")
	path := filepath.Join(d.MountPoint, r.Name)
	log.Debug("Path reported as: ", path)
	return volume.Response{Mountpoint: path}
}

func (d OpenstackDriver) Mount(r volume.MountRequest) volume.Response {
	d.Mutex.Lock()
	defer d.Mutex.Unlock()
	hostname, _ := os.Hostname()
	log.Infof("Mounting volume %+v on %s", r, hostname)
	vol, err := d.getVolByName(r.Name)
	if err != nil {
		log.Errorf("Failed to retrieve volume named: ", r.Name, "during Mount operation", err)
		return volume.Response{Err: err.Error()}
	}
	if vol.ID == "" {
		log.Error("Volume Not Found!")
		err := errors.New("Volume Not Found")
		return volume.Response{Err: err.Error()}
	}
	if vol.Status == "creating" {
		time.Sleep(time.Second * 5)
		vol, err = d.getVolByName(r.Name)
	}

	if err != nil {
		log.Errorf("Failed to retrieve volume named: ", r.Name, "during Mount operation", err)
		return volume.Response{Err: err.Error()}
	}

	vol, err = d.getVolByName(r.Name)
	if err != nil {
		log.Errorf("Error getting existing Volume by Name: (volume %s, error %s)", vol, err.Error())
		return volume.Response{Err: err.Error()}
	}

	compClient, err := openstack.NewComputeV2(d.Client, gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	})

	if vol.Status == "in-use" && os.Getenv("SWARM_MODE") == "true" {
		//attid := vol.Attachments[0]["attachment_id"].(string)
		//log.Debugf("AttachmentID: %+s", attid)
		instID := vol.Attachments[0]["server_id"].(string)
		log.Debugf("serverID: %+s", instID)
		log.Debug("Call gophercloud compute Detach...")
		aRes := volumeattach.Delete(compClient, instID, vol.ID)
		log.Debugf("Compute Detach results: %+v", aRes)

		time.Sleep(time.Duration(10) * time.Second)
		vol, err = d.getVolByName(r.Name)
		for i := 0; i < 300; i++ {
			if vol.Status == "available" {
				log.Debug("Volume ", vol.ID, "Successuflly detached")
				break
			}
			time.Sleep(time.Second)
			vol, err = d.getVolByName(r.Name)
		}
	}

	if vol.Status != "available" {
		log.Debugf("Volume info: %+v\n", vol)
		log.Errorf("Invalid volume status for Mount request, volume is: %s but must be available", vol.Status)
		err := errors.New("Volume in invalid state ")
		return volume.Response{Err: err.Error()}
	}

	aOpts := volumeattach.CreateOpts{
		VolumeID: vol.ID,
	}
	dat, err := ioutil.ReadFile("/sys/class/dmi/id/product_uuid")
	check(err)
	instID := strings.TrimSpace(strings.ToLower(string(dat)))
	log.Debug("Call gophercloud compute Attach...")
	aRes := volumeattach.Create(compClient, instID, aOpts)
	log.Debugf("Compute Attach results: %+v", aRes)

	device := "/dev/disk/by-id/virtio-" + vol.ID[0:20]
	waitForPathToExist(device, 60)
	if GetFSType(device) == "" {
		log.Debugf("Formatting device")
		err := FormatVolume(device, d.FSType)
		if err != nil {
			err := errors.New("Failed to format device")
			log.Error(err)
			return volume.Response{Err: err.Error()}
		}
	}

	if mountErr := Mount(device, d.MountPoint+"/"+r.Name); mountErr != nil {
		err := errors.New("Problem mounting docker volume ")
		log.Error(err)
		return volume.Response{Err: err.Error()}
	}

	return volume.Response{Mountpoint: d.MountPoint + "/" + r.Name}
}

func (d OpenstackDriver) Unmount(r volume.UnmountRequest) volume.Response {
	log.Infof("Unmounting volume: %+v", r)
	d.Mutex.Lock()
	defer d.Mutex.Unlock()
	vol, err := d.getVolByName(r.Name)
	if vol.ID == "" {
		log.Errorf("Request to Unmount failed because volume `%s` could not be found", r.Name)
		err := errors.New("Volume Not Found")
		return volume.Response{Err: err.Error()}
	}

	if err != nil {
		log.Errorf("Failed to retrieve volume named: `", r.Name, "` during Unmount operation", err)
		return volume.Response{Err: err.Error()}
	}

	if umountErr := Umount(d.MountPoint + "/" + r.Name); umountErr != nil {
		if umountErr.Error() == "Volume is not mounted" {
			log.Warning("Request to unmount volume, but it's not mounted")
			return volume.Response{}
		} else {
			return volume.Response{Err: umountErr.Error()}
		}
	}
	compClient, err := openstack.NewComputeV2(d.Client, gophercloud.EndpointOpts{
		Region: os.Getenv("OS_REGION_NAME"),
	})
	dat, err := ioutil.ReadFile("/sys/class/dmi/id/product_uuid")
	check(err)
	instID := strings.TrimSpace(strings.ToLower(string(dat)))
	pager := volumeattach.List(compClient, instID)
	var attid string
	err = pager.EachPage(func(page pagination.Page) (bool, error) {
		attachmentList, err := volumeattach.ExtractVolumeAttachments(page)
		if err != nil {
			log.Errorf("Get Attachment Error: %s", err)
			return false, err
		}

		for _, s := range attachmentList {
			if s.VolumeID == vol.ID {
				attid = s.ID
				return true, nil
			}
		}
		log.Error("Instance Not Found!")
		return false, errors.New("Instance Not Found")
	})
	if err != nil {
		log.Errorf("Extract attachment Error: %s", err)
	}

	log.Debug("Call gophercloud compute Detach...")
	aRes := volumeattach.Delete(compClient, instID, attid)
	log.Debugf("Compute Detach results: %+v", aRes)

	return volume.Response{}
}
