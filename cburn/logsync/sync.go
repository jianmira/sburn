package logsync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

//URLEntry is the URL to be explored
type URLEntry struct {
	url            *url.URL
	lastUpdateTime time.Time
}

//URLFetcher is url fetcher
type URLFetcher struct {
	ctx context.Context
	//cancel  context.CancelFunc
	wg      *sync.WaitGroup
	done    chan error
	id      int
	url     *url.URL
	subTree chan *URLEntry
	record  chan *URLEntry
	timeout time.Duration
	resp    *http.Response
	err     error
}

//Controller is the global data
type Controller struct {
	ctx               context.Context
	wg                sync.WaitGroup
	subTree           chan *URLEntry
	record            chan *URLEntry
	urlCache          map[string]*URLEntry
	heartbeatInterval time.Duration
	routineLauched    int
	routineReturned   int
	routineDone       chan error
}

//NewController returns new controller
func NewController(ctx context.Context) *Controller {
	return &Controller{
		ctx:               ctx,
		subTree:           make(chan *URLEntry),
		record:            make(chan *URLEntry),
		urlCache:          make(map[string]*URLEntry),
		heartbeatInterval: (time.Second),
		routineDone:       make(chan error),
	}
}

//NewURLEntry create new URL entry
func NewURLEntry(rawurl string) *URLEntry {
	url, _ := url.Parse(rawurl)
	return &URLEntry{
		url:            url,
		lastUpdateTime: time.Now(),
	}
}

//AddNewURLEntry adds new URL entry to the queue
func (c *Controller) AddNewURLEntry(urlEntry *URLEntry) {
	select {
	case <-c.ctx.Done():
		return
	case c.subTree <- urlEntry:
	}
}

//WaitJobDone waits for all child go routines exit
func (c *Controller) WaitJobDone() {
	fmt.Println("wait for job done")
	c.wg.Wait()
}

func (c *Controller) startURL(urlFetcher *URLFetcher) {
	c.wg.Add(1)
	go urlFetcher.run()
}

//StartExplorer starts explorer
func (c *Controller) StartExplorer() {
	c.wg.Add(1)
	go c.startExplorer()
}

func (c *Controller) startExplorer() {
	defer c.wg.Done()
loop:
	for {
		select {
		case <-c.ctx.Done():
			break loop
		case urlEntry := <-c.subTree:
			if c.urlNewerThanCache(urlEntry) {
				fmt.Printf("create new URL[%d] : %s", c.routineLauched, urlEntry.url.String())
				c.routineLauched++
				p := c.newURLFetcher(urlEntry.url)
				c.startURL(p)
			} else {
				fmt.Println("Existing URL :", urlEntry.url.String())
			}
		case <-c.routineDone:
			c.routineReturned++
			if c.routineReturned >= c.routineLauched {
				break loop
			}
		}

	}
	fmt.Printf("startExplorer Exit %d routine launched  ", c.routineReturned)
}

func (c *Controller) urlNewerThanCache(urlEntry *URLEntry) bool {
	//TBD
	urlCache, ok := c.urlCache[urlEntry.url.String()]
	if ok {
		_ = urlCache
		return false
	} else {
		c.urlCache[urlEntry.url.String()] = urlEntry
		return true
	}
}

func (c *Controller) newURLFetcher(url *url.URL) *URLFetcher {
	//ctx, cancel := context.WithCancel(c.ctx)

	return &URLFetcher{
		id:  c.routineLauched,
		ctx: c.ctx,
		//cancel:  cancel,
		done:    c.routineDone,
		wg:      &c.wg,
		url:     url,
		timeout: time.Second * 2,
		subTree: c.subTree,
		record:  c.record,
	}
}

//func (p *URLFetcher) stop() {
//p.cancel()
//}

func (p *URLFetcher) genNewURLRecord(urlEntry *URLEntry) {
	if urlEntry.url.Scheme == "http" || urlEntry.url.Scheme == "https" {
		select {
		case <-p.ctx.Done():
			return
		case p.subTree <- urlEntry:
		}
	}
}

func createURLEntryFromLink(t html.Token) (string, error) {
	for _, attr := range t.Attr {
		if attr.Key == "href" {
			return attr.Val, nil
		}
	}
	return "", errors.New("No Href attribute in the link")
}

func (p *URLFetcher) processPage() {
	defer p.resp.Body.Close()
	ct := p.resp.Header.Get("Content-Type")
	if strings.Contains(ct, "UTF-8") == false {
		return
	}
	z := html.NewTokenizer(p.resp.Body)
loop:
	for {
		select {
		case <-p.ctx.Done():
			break loop
		default:
			tt := z.Next()

			switch {
			case tt == html.ErrorToken:
				// End of the document, we're done
				break loop
			case tt == html.StartTagToken:
				t := z.Token()

				isAnchor := t.Data == "a"
				if isAnchor {
					l, err := createURLEntryFromLink(t)
					if err == nil {
						u, err := url.Parse(l)
						if err == nil {
							if u.IsAbs() {
								fmt.Println("abs :", u.String())
								p.genNewURLRecord(&URLEntry{u, time.Now()})
							} else {
								if len(u.Path) > 0 && !strings.Contains(p.url.Path, u.Path) {
									u = p.url.ResolveReference(u)
									fmt.Println("ref : ", u.String())
								}

								p.genNewURLRecord(&URLEntry{u, time.Now()})
							}
						}
					}
				}
			}
		}
	}
}

func (p *URLFetcher) onCompletion() {
	defer func() {
		p.done <- p.err
		p.wg.Done()
	}()
}

//Run starts the real work
func (p *URLFetcher) run() {
	defer p.onCompletion()

	client := &http.Client{}
	req, err := http.NewRequest("GET", p.url.String(), nil)
	if err != nil {
		p.err = err
		return
	}

	ctx, cancel := context.WithTimeout(p.ctx, p.timeout)
	defer cancel()
	req.WithContext(ctx)

	select {
	case <-ctx.Done():
		p.err = errors.New("Task Canccelled")
		return
	default:
		resp, err := client.Do(req)
		if err != nil {
			//fmt.Println("Error ", err)
			p.err = err
			return
		}
		p.resp = resp
		p.processPage()
	}
	return
}
