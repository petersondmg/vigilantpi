package main

import (
	"encoding/json"
	"os/exec"
)

func hddIsMounted() bool {
	if mountedDir == "" {
		return true
	}
	res, err := exec.Command("lsblk", "-o", "NAME,MOUNTPOINT", "--json").Output()
	if err != nil {
		logger.Println("error on mount cmd", err)
		return false
	}
	var resp struct {
		Devices []struct {
			Name       string `json:"name"`
			Mountpoint string `json:"mountpoint"`
			Children   []struct {
				Name       string `json:"name"`
				Mountpoint string `json:"mountpoint"`
			}
		} `json:"blockdevices"`
	}
	err = json.Unmarshal(res[:], &resp)
	if err != nil {
		logger.Println("cant unmarshal lsblk response:", err)
		return false
	}
	for _, device := range resp.Devices {
		if device.Mountpoint == mountedDir {
			return true
		}
		for _, child := range device.Children {
			if child.Mountpoint == mountedDir {
				return true
			}
		}
	}
	return false
}

func tryMount() {
	if mountDev == "" {
		return
	}
	if mountedDir == "" {
		logger.Println("no mount directory specified")
		return
	}
	logger.Println("trying to mount...")
	_, err := exec.Command(
		"mount",
		"-t",
		"vfat",
		"-o",
		"umask=0022,gid=1000,uid=1000",
		mountDev,
		mountedDir,
	).Output()
	if err != nil {
		logger.Println("error when trying to mount:", err)
		return
	}
	if config.PreventHDDSpindown {
		if config.MountDev == "" {
			logger.Printf("can't prevent hdd from spin down. mount_dev must be set")
			return
		}

		logger.Printf("preventing hdd from spinning down (hdparm)")

		if _, err := exec.Command("hdparm", "-B", "255", config.MountDev).Output(); err != nil {
			logger.Printf("err disabling power management from hdd: %s", err)
			return
		}

		if _, err := exec.Command("hdparm", "-S", "0", config.MountDev).Output(); err != nil {
			logger.Printf("err disabling hdd spindown timeout: %s", err)
			return
		}
	}
}
