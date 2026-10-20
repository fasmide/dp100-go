// dp100 prints a read-only snapshot of a DP100 using the built-in HID backend.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/fasmide/dp100-go"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("dp100", flag.ContinueOnError)
	flags.SetOutput(errOut)
	path := flags.String("device", "", "hidraw path (auto-detect if exactly one DP100 is present)")
	list := flags.Bool("list", false, "list DP100 interfaces without opening them")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if *list || *path == "" {
		devices, err := dp100.Discover()
		if err != nil {
			return err
		}
		if *list {
			return enc.Encode(devices)
		}
		if len(devices) != 1 {
			return fmt.Errorf("found %d DP100 interfaces; use -list and select -device", len(devices))
		}
		*path = devices[0].Path
	}
	client, err := dp100.Open(*path)
	if err != nil {
		return err
	}
	defer client.Close()
	info, err := client.Info(ctx)
	if err != nil {
		return err
	}
	settings, err := client.Settings(ctx)
	if err != nil {
		return err
	}
	system, err := client.System(ctx)
	if err != nil {
		return err
	}
	measurements, err := client.Measurements(ctx)
	if err != nil {
		return err
	}
	snapshot := struct {
		Info         dp100.DeviceInfo
		Settings     dp100.OutputSettings
		System       dp100.SystemSettings
		Measurements dp100.Measurements
	}{info, settings, system, measurements}
	return enc.Encode(snapshot)
}
