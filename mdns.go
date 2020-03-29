package main

import (
	"errors"
	"net"
	"os"

	"github.com/hashicorp/mdns"
)

func mdnsServer() {
	ips, err := getLocalIP()
	if err != nil {
		logger.Printf("err getting ip for mdns: %s", err)
		return
	}

	host, _ := os.Hostname()

	logger.Printf("starting mdns server for host: %s", host)

	service, err := mdns.NewMDNSService(host, "_foobar._tcp", "", "", 80, ips, []string{"VigilantPI Admin"})
	if err != nil {
		logger.Printf("error NewMDNSService: %s", err)
	}

	// Create the mDNS server, defer shutdown
	server, err := mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		logger.Printf("error creating mdns server: %s", err)
	}
	//defer server.Shutdown()
	_ = server
}

func getLocalIP() (ips []net.IP, err error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	for _, address := range addrs {
		// check the address type and if it is not a loopback the display it
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP)
			}
		}
	}
	if len(addrs) == 0 {
		err = errors.New("can't find any ip")
	}
	return
}
