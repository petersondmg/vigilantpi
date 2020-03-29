package main

import (
	"fmt"
	"os/exec"
)

func setWifi(ssid, pass string) {
	logger.Println("setting wifi to", ssid, pass)
	_, err := exec.Command("sh", "-c", fmt.Sprintf("wpa_passphrase '%s' '%s' > /etc/wpa_supplicant/wpa_supplicant-wlan0.conf", ssid, pass)).Output()
	if err != nil {
		logger.Println("error wpa_passphrase cmd", err)
		return
	}
	logger.Println("wifi updated")
}
