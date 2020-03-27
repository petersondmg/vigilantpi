build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6 go build -o vigilantpi -ldflags "-X main.version=$(version)"

release: build
	tar -czvf vigilantpi.tar.gz vigilantpi
