## VigilantPI

Vigilant is an nvr system for IP cameras, having mainly RaspberryPI as target.

It can record any URL supported by *ffmpeg*.
It provides some HTTP hooks that can be used to deal with IP camera's instabilities


### Sample config.yaml:
```yaml

ffmpeg: /usr/bin/ffmpeg

mount_dir: /mnt/hdd
mount_dev: /dev/sda1
prevent_hdd_spindown: true

admin:
  user: ""
  pass: ""
  addr: :80

videos_dir: /mnt/hdd/cameras

duration: 30m0s

cameras:
- name: main street 
  url: rtsp://admin:admin@192.168.1.2:554/onvif1

- name: front yard
  url: rtsp://192.168.1.4:10554/udp/av0_1
  pre_rec_urls:
  - url: http://192.168.1.4/decoder_control.cgi?loginuse=admin&loginpas=admin&command=31&onestep=0&sit=31
    method: get
    basic_user: admin
    basic_pass: admin
    headers: []
    expect: result="ok"
    desc: return camera to main position

wifi_ssid: "My Home WIFI"
wifi_pass: "dontenter"

cron:
- every: [2h]
  hooks:
  - url: http://192.168.1.4/reboot.cgi?&loginuse=admin&loginpas=admin
    method: get
    basic_user: admin
    basic_pass: admin
    headers: []
    expect: result="ok"
    desc: reboot camera


raspberry_pi:
  led_pin: 19
```


Raspberry PI dependencies:
- [ffmpeg](https://wiki.archlinux.org/index.php/FFmpeg)
- [hdparm](https://wiki.archlinux.org/index.php/hdparm) when using `prevent_hdd_spindown` option on config.yaml 

