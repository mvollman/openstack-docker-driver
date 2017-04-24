package main

import (
	"flag"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/docker/go-plugins-helpers/volume"
	"os"
)

const (
	VERSION = "0.1.1"
)

func main() {
	version := flag.Bool("version", false, "Display version and exit")
	flag.Parse()
	if *version == true {
		fmt.Println("Version: ", VERSION)
		os.Exit(0)
	}

	debug := flag.Bool("debug", true, "Enable debug logging")
	flag.Parse()
	if *debug == true {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}
	log.Info("Starting openstack-docker-driver version: ", VERSION)
	d := New()
	h := volume.NewHandler(d)
	log.Info(h.ServeUnix("openstack", 0))
}
