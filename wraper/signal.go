package wraper

import (
	"context"
	"os"
	"os/signal"
)

//SignalWraper for graceful kill
func SignalWraper(ctx context.Context) (context.Context, context.CancelFunc) {
	c := make(chan os.Signal, 1)
	ctx, cancel := context.WithCancel(ctx)

	signal.Notify(c, os.Interrupt)
	go func() {
		for {
			select {
			case <-c:
				cancel()
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return ctx, cancel
}
