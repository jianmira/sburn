package logsync_test

import (
	"testing"

	"github.com/jianmira/sburn/cburn/logsync"
)

func TestSyncURL(t *testing.T) {
	url := "http://www.supermicro.com"

	c := logsync.NewController()

	p := logsync.NewURLFetcher(c, url)
	c.StartURL(p)
}
