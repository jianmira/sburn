package logsync_test

import (
	"context"
	"testing"

	"github.com/jianmira/sburn/cburn/logsync"
	"github.com/jianmira/sburn/wraper"
)

func TestSyncURL(t *testing.T) {
	ctx, cancel := wraper.SignalWraper(context.Background())
	defer cancel()
	u := logsync.NewURLEntry("http://10.2.1.154/burnin/")
	c := logsync.NewController(ctx)
	c.StartExplorer()
	c.AddNewURLEntry(u)
	c.WaitJobDone()
}
