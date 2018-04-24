package ping_test

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"testing"
	"time"

	"github.com/jianmira/sburn/net/ping"
)

type result struct {
	host     string
	packRecv int
}

func testping(ctx context.Context, subnet string) {
	var pingwg sync.WaitGroup

	result := make(chan *result, 256)

	for i := 1; i < 255; i++ {
		pingwg.Add(1)
		go pingHost(ctx, fmt.Sprintf("%s.%d", subnet, i), result, &pingwg)
	}
	pingwg.Wait()
	close(result)
	for r := range result {
		if r.packRecv == 1 {
			fmt.Println(r.host, " detected ")
		}
	}
}

func pingHost(ctx context.Context, host string, ch chan<- *result, pingwg *sync.WaitGroup) {
	defer pingwg.Done()

	pinger, err := ping.NewPinger(ctx, host)
	if err != nil {
		fmt.Printf("ERROR: %s\n", err.Error())
		return
	}
	defer func() {
		ch <- &result{host, pinger.PacketsRecv}
	}()
	/*
		pinger.OnRecv = func(pkt *ping.Packet) {

			fmt.Printf("%d bytes from %s: icmp_seq=%d time=%v\n",
				pkt.Nbytes, pkt.IPAddr, pkt.Seq, pkt.Rtt)

		}
		pinger.OnFinish = func(stats *ping.Statistics) {

			fmt.Printf("\n--- %s ping statistics ---\n", stats.Addr)
			fmt.Printf("%d packets transmitted, %d packets received, %v%% packet loss\n",
				stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss)
			fmt.Printf("round-trip min/avg/max/stddev = %v/%v/%v/%v\n",
				stats.MinRtt, stats.AvgRtt, stats.MaxRtt, stats.StdDevRtt)

		}
	*/
	pinger.Count = 1
	pinger.Interval = time.Second * 100000
	pinger.Timeout = time.Second
	pinger.SetPrivileged(false)

	pinger.Run()
}

func TestPing(t *testing.T) {
	var S = 19
	var N = 1
	c := make(chan os.Signal, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
loop:
	for i := S; i < S+N; i++ {
		select {
		case <-ctx.Done():
			fmt.Println("task cancelled")
			break loop
		default:
			subnet := fmt.Sprintf("10.125.%d", i)
			testping(ctx, subnet)
			fmt.Printf(" scan subnet : %s\n", subnet)
		}
	}
}
