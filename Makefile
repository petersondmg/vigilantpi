arm-build:
	docker run --rm -it -v "$$PWD":/go/src/vigilant_pi \
		-w /go/src/vigilant_pi \
		-e CGO_ENABLED=1 -e GOOS=linux -e GOARCH=arm -e GOARM=5 \
		-e CC=arm-linux-gnueabihf-gcc anakros/goarm \
		go build -o vigilantpi

ship:
	scp vigilantpi pi@raspberrypi.local:~/.

ship-tmp:
	scp vigilantpi pi@raspberrypi.local:~/vigilantpi-tmp

build-and-ship: arm-build ship
