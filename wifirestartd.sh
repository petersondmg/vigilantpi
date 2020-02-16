#!/bin/sh

while true ; do
	if ifconfig wlan0 | egrep -q 'inet\s?[0-9]{1,3}\.+' ; then
		sleep 60
	else
		echo "Network connection down! Attempting reconnection."
		ifup --force wlan0
		sleep 10
	fi
done
