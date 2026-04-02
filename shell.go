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
	res := execString("tail", "-n", "50", logPath)
	lines := strings.Split(strings.TrimSpace(res), "\n")
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n")
}
