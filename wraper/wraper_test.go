package wraper_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/jianmira/sburn/wraper"
)

func TestSignalWrap(t *testing.T) {
	ctx, cancel := wraper.SignalWraper(context.Background())
	defer cancel()

	<-ctx.Done()
	fmt.Println("Task cancelled")

}
