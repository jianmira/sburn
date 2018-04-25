package logsync

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"
	"time"
)

//URLEntry is the URL to be explored
type URLEntry struct {
	url            string
	lastUpdateTime time.Time
}

//Controller is the global data
type Controller struct {
	Ctx     context.Context
	Wg      sync.WaitGroup
	SubTree chan URLEntry
	Record  chan URLEntry
}

//NewController returns new controller
func NewController() *Controller {
	return &Controller{
		Ctx:     context.Background(),
		SubTree: make(chan URLEntry),
		Record:  make(chan URLEntry),
	}
}

//StartURL starts URL explore
func (c *Controller) StartURL(urlFetcher *URLFetcher) {
	c.Wg.Add(1)
	urlFetcher.run()
	c.Wg.Wait()
}

//URLFetcher is url fetcher
type URLFetcher struct {
	ctx     context.Context
	cancel  context.CancelFunc
	wg      *sync.WaitGroup
	url     string
	timeout time.Duration
	leaf    bool
	page    []byte
	urls    []string
	resp    *http.Response
	err     error
}

//NewURLFetcher returns a URL Fetcher
func NewURLFetcher(c *Controller, url string) *URLFetcher {
	ctx, cancel := context.WithCancel(c.Ctx)
	return &URLFetcher{
		ctx:     ctx,
		cancel:  cancel,
		wg:      &c.Wg,
		url:     url,
		timeout: time.Second * 2,
	}
}

func (p *URLFetcher) stop() {
	p.cancel()
}

func (p *URLFetcher) processPage() {
	defer p.resp.Body.Close()
	//TBD
	out, _ := ioutil.ReadAll(p.resp.Body)
	fmt.Printf("Server Response: %s\n", out)

}

//Run starts the real work
func (p *URLFetcher) run() error {
	defer p.wg.Done()
	defer p.cancel()

	client := &http.Client{}
	req, _ := http.NewRequest("GET", p.url, nil)
	ctx, cancel := context.WithTimeout(p.ctx, p.timeout)
	defer cancel()
	req.WithContext(ctx)

	select {
	case <-ctx.Done():
		return errors.New("Task Canccelled")
	default:
		resp, err := client.Do(req)
		if err != nil {
			fmt.Println("Error ", err)
			return err
		}
		p.resp = resp
		p.processPage()
	}
	return nil
}
