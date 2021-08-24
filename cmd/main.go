package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	app "github.com/mwennrich/gardener-slacker/cmd/gardenerslacker"
	"k8s.io/klog"
)

func main() {

	// Setup signal handler.
	signalCh := make(chan os.Signal, 2)
	ctx, cancel := context.WithCancel(context.Background())
	signal.Notify(signalCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer cancel()

	go func() {
		<-signalCh
		klog.Info("Received interupt signal.")
		signal.Stop(signalCh)
		cancel()
	}()

	// Init and run app.
	command := app.NewStartGardenerSlacker(ctx)
	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}
