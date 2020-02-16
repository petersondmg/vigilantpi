build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=5 go build -o vigilantpi -ldflags "-X main.version=$(version)"

