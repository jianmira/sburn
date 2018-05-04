package logsync_test

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/jianmira/sburn/cburn/logsync"
	"github.com/jianmira/sburn/wraper"
	"github.com/mongodb/mongo-go-driver/mongo"
	"github.com/stretchr/testify/require"
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

func TestProcURL(t *testing.T) {
	ctx, cancel := wraper.SignalWraper(context.Background())
	defer cancel()
	u := logsync.NewURLEntry("http://10.2.1.154/burnin/0c-c4-7a-a5-a8-c2/")
	c := logsync.NewController(ctx)
	c.StartCburnProcessor()
	c.AddNewRecordURLEntry(u)
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

func TestDocumentationExamples(t *testing.T) {
	client, err := mongo.NewClient("mongodb://127.0.0.1:27017")
	if err != nil {
		require.NoError(t, err)
	}
	db := client.Database("documentation_examples")

	logsync.InsertExamples(t, db)
	/*
		logsync.QueryToplevelFieldsExamples(t, db)
		logsync.QueryEmbeddedDocumentsExamples(t, db)
		logsync.QueryArraysExamples(t, db)
		logsync.QueryArrayEmbeddedDocumentsExamples(t, db)
		logsync.QueryNullMissingFieldsExamples(t, db)
		logsync.ProjectionExamples(t, db)
		logsync.UpdateExamples(t, db)
		logsync.DeleteExamples(t, db)
	*/
}
