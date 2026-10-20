package dp100_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/fasmide/dp100-go"
)

func ExampleDiscover() {
	devices, err := dp100.Discover()
	if err != nil {
		log.Print(err)
		return
	}
	for _, device := range devices {
		fmt.Printf("%s: %s (USB serial %s)\n", device.Path, device.Name, device.Serial)
	}
}

func ExampleClient_Measurements() {
	psu, err := dp100.Open("/dev/hidraw14")
	if err != nil {
		log.Print(err)
		return
	}
	defer psu.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	m, err := psu.Measurements(ctx)
	if err != nil {
		log.Print(err)
		return
	}
	fmt.Printf("%.3f V, %.3f A, %s\n", float64(m.OutputVoltageMV)/1000, float64(m.OutputCurrentMA)/1000, m.Mode)
}

func ExampleClient_SetEnabled() {
	psu, err := dp100.Open("/dev/hidraw14")
	if err != nil {
		log.Print(err)
		return
	}
	defer psu.Close()
	// Close alone does not disable output. Explicitly request OFF and check ACK.
	if err := psu.SetEnabled(context.Background(), false); err != nil {
		var op *dp100.OpError
		if errors.As(err, &op) && op.Transmitted {
			log.Print("device state uncertain; inspect/read back before proceeding")
		}
		log.Print(err)
	}
}
