package main

import (
	"os/exec"
	"strings"
)

var shellR = strings.NewReplacer(
	"$", "",
	"`", "",
	"!", "",
	"(", "",
	")", "",
)

func safeShell(s string) string {
	return shellR.Replace(s)
}

func execString(cmd string, args ...string) string {
	res, err := exec.Command(cmd, args...).Output()
	if err != nil {
		return err.Error()
	}
	return string(res)
}

func serverDate() string {
	return execString("date")
}

func serverDF() string {
	return execString("df", "-H")
}

func serverLog() string {
	return execString("tail", "-n", "50", logPath)
}
