package logsync_test

import (
	"context"
	"fmt"
	"regexp"
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
	c.StartCburnProcessor()
	c.AddNewURLEntry(u)
	c.WaitJobDone()
}

func TestURLTime(t *testing.T) {
	s := "                 18-Apr-2016 08:39    -"
	logsync.CreateTime(s)
}

func TestMatch(t *testing.T) {
	s := "SMN=\"Supermicro\"\nSPN=\"SYS-6019U-TR4T\"\n"
	//s1 := "Key=Value"
	r, _ := regexp.Compile(`([\w-]+)=\"([\w-]+)\"`)
	attrs := r.FindAllString(s, -1)
	for _, attr := range attrs {
		p := r.FindStringSubmatch(attr)
		fmt.Printf("%v\n", p)
	}
	fmt.Println(s)

}
