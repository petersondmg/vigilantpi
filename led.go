package main

import (
	"time"

	"github.com/stianeikeland/go-rpio/v4"
)

const (
	blinkInterval = time.Second
)

func setupLED(ledPin int) func() error {
	pin := rpio.Pin(ledPin)
	if err := rpio.Open(); err != nil {
		logger.Println("Error setuping LED:", err)
		return func() error {
			return nil
		}
	}
	pin.Output()

	var ticker *time.Ticker
	times := make(chan int)
	stop := make(chan struct{})

	go func() {
		for blinks := range times {
			if ticker != nil {
				stop <- struct{}{}
				ticker.Stop()
				ticker = nil
			}

			if blinks == 0 {
				continue
			}

			ticker = time.NewTicker(blinkInterval)
			go func() {
				var on bool
				pin.Low()

				for {
					select {
					case <-ticker.C:
						if on {
							on = false
							pin.Low()
							continue
						}
						on = true

						iterations := blinks * 2
						stateDuration := blinkInterval / time.Duration(iterations)

						for i := 0; i < iterations; i++ {
							pin.Toggle()
							time.Sleep(stateDuration)
						}
					case <-stop:
						return
					}
				}
			}()
		}
	}()

	led.BadHD = func() {
		times <- 1
	}
	led.BadCamera = func() {
		times <- 2
	}

	led.BadNetwork = func() {
		times <- 3
	}
	led.Confirm = func() {
		times <- 0
		for i := 0; i < 10; i++ {
			pin.Toggle()
			time.Sleep(time.Millisecond * 200)
		}
	}
	led.On = func() {
		times <- 0
		pin.High()
	}
	led.Off = func() {
		times <- 0
		pin.Low()
	}
	return func() error {
		times <- 0
		led.Off()
		return rpio.Close()
	}
}
