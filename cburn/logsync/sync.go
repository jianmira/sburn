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

type pack struct {
	r   *http.Response
	err error
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

	c := make(chan *pack, 1)
	defer close(c)

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

/*
func (p *URLFetcher) run2() error {
	defer p.wg.Done()
	defer p.cancel()

	var wg sync.WaitGroup

	c := make(chan *pack, 1)
	defer close(c)

	wg.Add(1)
	go func() {
		defer wg.Done()
		client := &http.Client{}
		req, _ := http.NewRequest("GET", p.url, nil)
		ctx, cancel := context.WithTimeout(p.ctx, p.timeout)
		defer cancel()
		req.WithContext(ctx)

		select {
		case <-ctx.Done():
			return
		default:
			resp, err := client.Do(req)
			pack := &pack{resp, err}
			select {
			case <-ctx.Done():
			default:
				c <- pack
			}
		}
	}()

	select {
	case <-p.ctx.Done():
		wg.Wait()
		return errors.New("Task Canccelled")
	case ok := <-c:
		wg.Wait()
		err := ok.err
		resp := ok.r
		if err != nil {
			fmt.Println("Error ", err)
			return err
		}
		p.resp = resp
		p.processPage()
	}
	return nil
}
*/
