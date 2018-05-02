package logsync

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
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

// URLQueue is a basic FIFO queue based on a circular list that resizes as needed.
type urlQueue struct {
	nodes []*URLEntry
	size  int
	head  int
	tail  int
	count int
}

//NewURLQueue returns a new queue with the given initial size.
func newURLQueue(size int) *urlQueue {
	return &urlQueue{
		nodes: make([]*URLEntry, size),
		size:  size,
	}
}

// Push adds a node to the queue.
func (q *urlQueue) Push(n *URLEntry) {
	if q.head == q.tail && q.count > 0 {
		nodes := make([]*URLEntry, len(q.nodes)+q.size)
		copy(nodes, q.nodes[q.head:])
		copy(nodes[len(q.nodes)-q.head:], q.nodes[:q.head])
		q.head = 0
		q.tail = len(q.nodes)
		q.nodes = nodes
	}
	q.nodes[q.tail] = n
	q.tail = (q.tail + 1) % len(q.nodes)
	q.count++
}

// Pop removes and returns a node from the queue in first to last order.
func (q *urlQueue) Pop() *URLEntry {
	if q.count == 0 {
		return nil
	}
	node := q.nodes[q.head]
	q.head = (q.head + 1) % len(q.nodes)
	q.count--
	return node
}

//Fetcher is the base class for *Fetcher
type Fetcher struct {
	id       int
	client   *clientConn
	urlEntry *URLEntry
	ctx      context.Context
	done     chan *Fetcher
	timeout  time.Duration
	//resp     *http.Response
	//err      error
}

//URLFetcher is url fetcher
type URLFetcher struct {
	Fetcher
	subTree chan *URLEntry
	record  chan *URLEntry
}

//RecordFetcher is Record fetcher
type RecordFetcher struct {
	Fetcher
	subUrls     *urlQueue
	files       map[string][]byte
	recordAttrs map[string]string
}

type routineStat struct {
	routineLauched  int
	routineReturned int
	routineDone     chan *Fetcher
	routineMax      int
}

//Controller is the global data
type Controller struct {
	ctx          context.Context
	wg           sync.WaitGroup
	client       *clientConn
	subTree      chan *URLEntry
	record       chan *URLEntry
	urlCache     map[string]*URLEntry
	explorerStat *routineStat
	recordStat   *routineStat
}
type clientConn struct {
	client       *http.Client
	maxPoolSize  int
	cSemaphore   chan int
	reqPerSecond int
	rateLimiter  *time.Ticker
}

func (c *clientConn) Do(req *http.Request) (*http.Response, error) {
	if c.maxPoolSize > 0 {
		c.cSemaphore <- 1 // Grab a connection from our pool
		defer func() {
			<-c.cSemaphore // Defer release our connection back to the pool
		}()
	}

	if c.reqPerSecond > 0 {
		<-c.rateLimiter.C // Block until a signal is emitted from the rateLimiter
	}

	resp, err := c.client.Do(req)
	return resp, err
}

func newClientConn(maxPoolSize int, reqPerSecond int) *clientConn {
	var cSemaphore chan int
	var rateLimiter *time.Ticker

	if maxPoolSize > 0 {
		cSemaphore = make(chan int, maxPoolSize)
	}
	if reqPerSecond > 0 {
		rateLimiter = time.NewTicker(time.Second / time.Duration(reqPerSecond))
	}
	return &clientConn{
		client:       &http.Client{},
		maxPoolSize:  maxPoolSize,
		cSemaphore:   cSemaphore,
		reqPerSecond: reqPerSecond,
		rateLimiter:  rateLimiter,
	}

}

func newRoutineStat(max int) *routineStat {
	return &routineStat{
		routineDone: make(chan *Fetcher),
		routineMax:  max,
	}
}

//NewController returns new controller
func NewController(ctx context.Context) *Controller {
	return &Controller{
		ctx:          ctx,
		client:       newClientConn(5000, 0),
		subTree:      make(chan *URLEntry, 1000),
		record:       make(chan *URLEntry, 1000),
		urlCache:     make(map[string]*URLEntry),
		explorerStat: newRoutineStat(0),
		recordStat:   newRoutineStat(0),
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
	//fmt.Println("wait for job done")
	c.wg.Wait()
}

func (c *Controller) startURL(urlFetcher *URLFetcher) {
	go urlFetcher.run()
}

func (c *Controller) startRecord(recordFether *RecordFetcher) {
	go recordFether.run()
}

//StartExplorer starts explorer
func (c *Controller) StartExplorer() {
	c.wg.Add(1)
	go c.startExplorer()
}

func (c *Controller) startExplorer() {
	defer c.wg.Done()
	s := c.explorerStat
loop:
	for {
		select {
		case <-c.ctx.Done():
			select {
			case fetcher := <-s.routineDone:
				s.routineReturned++
				fmt.Printf("-Explorer|End[ %d| %d] : %s\n", fetcher.id, s.routineLauched-s.routineReturned, fetcher.urlEntry.url.String())
			default:
				if s.routineReturned >= s.routineLauched {
					break loop
				}
			}

		case fetcher := <-s.routineDone:
			s.routineReturned++
			if s.routineReturned >= s.routineLauched {
				fmt.Printf("Explorer|End[ %d| %d] : %s\n", fetcher.id, s.routineLauched-s.routineReturned, fetcher.urlEntry.url.String())
				break loop
			}
		default:
			if s.routineMax == 0 || s.routineLauched-s.routineReturned < s.routineMax {
				select {
				case urlEntry := <-c.subTree:
					if c.urlNewerThanCache(urlEntry) {
						fmt.Printf("Explorer|Start[ %d| %d] : %s %s\n", s.routineLauched, s.routineLauched-s.routineReturned, urlEntry.url.String(), urlEntry.lastUpdateTime)
						s.routineLauched++
						p := c.newURLFetcher(urlEntry)
						c.startURL(p)
					}
				default:
				}
			}
		}

	}
	fmt.Printf("\nCburn Explorer Summary : %d routine launched  %d routine returned\n", s.routineLauched, s.routineReturned)
}

//StartCburnProcessor starts record process
func (c *Controller) StartCburnProcessor() {
	c.wg.Add(1)
	go c.startCburnProcessor()
}

func (c *Controller) startCburnProcessor() {
	defer c.wg.Done()
	s := c.recordStat
loop:
	for {
		select {
		case <-c.ctx.Done():
			select {
			case fetcher := <-s.routineDone:
				fmt.Printf("-Processor|End[ %d| %d] : %s\n", fetcher.id, s.routineLauched-s.routineReturned, fetcher.urlEntry.url.String())
				s.routineReturned++
			default:
				if s.routineReturned >= s.routineLauched {
					break loop
				}
			}
		case fetcher := <-s.routineDone:
			s.routineReturned++
			fmt.Printf("Processor|End[ %d| %d] : %s\n", fetcher.id, s.routineLauched-s.routineReturned, fetcher.urlEntry.url.String())
		default:
			if s.routineMax == 0 || s.routineLauched-s.routineReturned < s.routineMax {
				select {
				case record, ok := <-c.record:
					if ok {
						s.routineLauched++
						fmt.Printf("Processor|Start[ %d| %d] : %s %s\n", s.routineLauched, s.routineLauched-s.routineReturned, record.url.String(), record.lastUpdateTime)
						r := c.newRecordFetcher(record)
						c.startRecord(r)
					} else {
						if s.routineReturned >= s.routineLauched {
							break loop
						}
						select {
						case <-s.routineDone:
							s.routineReturned++
						default:
							if s.routineReturned >= s.routineLauched {
								break loop
							}
						}
					}
				default:
				}
			}
		}

	}
	fmt.Printf("\nCburn Proccessor Summary : %d routine launched  %d routine returned\n", s.routineLauched, s.routineReturned)
}

func (c *Controller) urlNewerThanCache(urlEntry *URLEntry) bool {
	//TBD
	urlCache, ok := c.urlCache[urlEntry.url.String()]
	if ok {
		if urlEntry.lastUpdateTime.Before(urlCache.lastUpdateTime) {
			return true
		}
		return false
	} else {
		c.urlCache[urlEntry.url.String()] = urlEntry
		return true
	}
}

func (c *Controller) newURLFetcher(urlEntry *URLEntry) *URLFetcher {
	//ctx, cancel := context.WithCancel(c.ctx)

	return &URLFetcher{
		Fetcher: Fetcher{
			id:       c.explorerStat.routineLauched,
			client:   c.client,
			ctx:      c.ctx,
			done:     c.explorerStat.routineDone,
			urlEntry: urlEntry,
			timeout:  time.Second * 2,
		},
		subTree: c.subTree,
		record:  c.record,
	}
}

func (c *Controller) newRecordFetcher(urlEntry *URLEntry) *RecordFetcher {
	r := &RecordFetcher{
		Fetcher: Fetcher{
			id:       c.recordStat.routineLauched,
			client:   c.client,
			ctx:      c.ctx,
			done:     c.recordStat.routineDone,
			urlEntry: urlEntry,
			timeout:  time.Second * 2,
		},
		subUrls:     newURLQueue(100),
		files:       make(map[string][]byte),
		recordAttrs: make(map[string]string),
	}
	r.genNewRecordEntry(urlEntry)
	return r
}

func (p *URLFetcher) genNewURLEntry(urlEntry *URLEntry) {
	if urlEntry.url.Scheme == "http" || urlEntry.url.Scheme == "https" {
		select {
		case <-p.ctx.Done():
			return
		case p.subTree <- urlEntry:
			return
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

//CreateTime builds url time
func CreateTime(ts string) time.Time {
	regex, _ := regexp.Compile("([0-9]+)-([A-Za-z]+)-[0-9]{2}([0-9]{2})[\t ]*([0-9]{2}:[0-9]{2})")
	tss := regex.FindStringSubmatch(ts)
	if len(tss) == 5 {
		ts = fmt.Sprintf("%s %s %s %s PDT", tss[1], tss[2], tss[3], tss[4])
		tm, _ := time.Parse(time.RFC822, ts)
		return tm
	} else {
		tm := time.Now()
		return tm
	}

}

func getURLTime(z *html.Tokenizer) (time.Time, html.TokenType) {
	tt := z.Token().Type

	for tt != html.EndTagToken {
		if tt == html.ErrorToken {
			return time.Now(), tt
		}
		tt = z.Next()
	}

	for tt != html.TextToken {
		if tt == html.ErrorToken {
			return time.Now(), tt
		}
		tt = z.Next()
	}

	tm := CreateTime(string(z.Text()))

	return tm, tt
}

func isCburnFolder(urlEntries []*URLEntry) bool {
	for _, urlEntry := range urlEntries {
		if strings.HasSuffix(urlEntry.url.String(), "stage1.conf") ||
			strings.HasSuffix(urlEntry.url.String(), "stage2.conf") {
			return true
		}
	}
	return false
}

func (p *URLFetcher) dispatchURLs(urlEntries []*URLEntry) {
	for _, urlEntry := range urlEntries {
		if strings.HasSuffix(urlEntry.url.String(), "/") {
			p.genNewURLEntry(urlEntry)
		}
	}
}

func (p *URLFetcher) dispatchCburn() {
	fmt.Println("Dispatch cburn : ", p.urlEntry.url.String())
	select {
	case <-p.ctx.Done():
		return
	case p.record <- p.urlEntry:
		return
	}
}

func (p *URLFetcher) processPage(resp *http.Response) {

	var urls []*URLEntry
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "UTF-8") == false {
		resp.Body.Close()
		return
	}
	z := html.NewTokenizer(resp.Body)

	func() {
		defer resp.Body.Close()
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
								var tm time.Time
								if u.IsAbs() {
									tm, tt = getURLTime(z)
									urls = append(urls, &URLEntry{u, tm})
								} else {
									if len(u.Path) > 0 && !strings.Contains(p.urlEntry.url.Path, u.Path) {
										u = p.urlEntry.url.ResolveReference(u)
										tm, tt = getURLTime(z)
										urls = append(urls, &URLEntry{u, tm})
										//fmt.Println(u.String())
									}
								}
							}
						}
					}
					if tt == html.ErrorToken {
						break loop
					}
				}
			}
		}
	}()

	if isCburnFolder(urls) {
		p.dispatchCburn()
	} else {
		p.dispatchURLs(urls)
	}

}

func (p *URLFetcher) onCompletion() {
	p.done <- &p.Fetcher
}

//Run starts the real work
func (p *URLFetcher) run() {
	defer p.onCompletion()
	req, err := http.NewRequest("GET", p.urlEntry.url.String(), nil)
	if err != nil {
		//wrong format, don't have to reinsert the URL for retry
		//p.err = err
		return
	}
	//req.Header.Set("Connection", "close")
	ctx, cancel := context.WithTimeout(p.ctx, p.timeout)
	defer cancel()
	req.WithContext(ctx)

	select {
	case <-ctx.Done():
		return
	default:
		resp, err := p.client.Do(req)
		if err != nil {
			fmt.Println("Http Connection Error (Retry Scheduled) : ", err)
			p.genNewURLEntry(p.urlEntry)
			//p.err = err
			return
		}
		p.processPage(resp)
	}
}

func (p *RecordFetcher) genNewRecordEntry(urlEntry *URLEntry) {
	p.subUrls.Push(urlEntry)
}

func (p *RecordFetcher) processPage(urlEntry *URLEntry, resp *http.Response) {
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "UTF-8") == false {
		return
	}
	z := html.NewTokenizer(resp.Body)
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
							var tm time.Time
							if u.IsAbs() {
								tm, tt = getURLTime(z)
								p.subUrls.Push(&URLEntry{u, tm})

							} else {
								//fmt.Printf("Checking %s under %s\n", u.String(), urlEntry.url.Path)
								if len(u.Path) > 0 && !strings.Contains(urlEntry.url.Path, u.Path) {
									u = urlEntry.url.ResolveReference(u)
									tm, tt = getURLTime(z)
									p.subUrls.Push(&URLEntry{u, tm})
									//fmt.Println("Push :", u.String())
								}
							}
						}
					}
				}
				if tt == html.ErrorToken {
					break loop
				}
			}
		}
	}
}

func (p *RecordFetcher) processFile(filepath string, resp *http.Response) {
	defer resp.Body.Close()
	data, _ := ioutil.ReadAll(resp.Body)
	p.files[filepath] = data
}

func (p *RecordFetcher) onCompletion() {
	p.done <- &p.Fetcher
}

func (p *RecordFetcher) runSingle() (bool, bool) {

	urlEntry := p.subUrls.Pop()
	if urlEntry == nil {
		fmt.Println("Finished :", p.urlEntry.url.String())
		return true, true
	}

	if !strings.HasPrefix(urlEntry.url.String(), p.urlEntry.url.String()) {
		return false, false
	}

	req, err := http.NewRequest("GET", urlEntry.url.String(), nil)

	if err != nil {
		//p.err = err
		return false, false
	}
	//req.Header.Set("Connection", "close")
	ctx, cancel := context.WithTimeout(p.ctx, p.timeout)
	defer cancel()
	req.WithContext(ctx)

	select {
	case <-ctx.Done():
		return false, true
	default:
		resp, err := p.client.Do(req)
		if err != nil {
			fmt.Println("Http Connection Error (Retry Scheduled) : ", err)
			p.subUrls.Push(urlEntry)
			//p.err = err
			return false, false
		}
		if strings.HasSuffix(urlEntry.url.String(), "/") {
			fmt.Println("Parse Page ", urlEntry.url.String())
			p.processPage(urlEntry, resp)
		} else {
			filepath := strings.TrimPrefix(urlEntry.url.String(), p.urlEntry.url.String())
			p.processFile(filepath, resp)
			fmt.Println("Downloaded File ", filepath, " ", p.urlEntry.url.Path)
		}

	}
	return false, false
}

func (p *RecordFetcher) getIns() {
	r, _ := regexp.Compile(`([\w-]+)=\"([\w-]+)\"`)
	for name, data := range p.files {
		if strings.HasPrefix(name, "ins-") {
			attrs := r.FindAllString(string(data), -1)
			for _, attr := range attrs {
				ri := r.FindStringSubmatch(attr)
				p.recordAttrs[ri[1]] = ri[2]
			}
		}
	}
	data, ok := p.files["sysconf.cfg"]
	if ok {
		attrs := r.FindAllString(string(data), -1)
		for _, attr := range attrs {
			ri := r.FindStringSubmatch(attr)
			p.recordAttrs[ri[1]] = ri[2]
		}
		fmt.Printf("Found : %s/%s\n", p.recordAttrs["BOARD_NAME"], p.recordAttrs["SYSTEM_PRODUCT_NAME"])
	}
	return
}

func (p *RecordFetcher) run() {
	defer p.onCompletion()
loop:
	for {
		select {
		case <-p.ctx.Done():
			break loop
		default:
			if finished, exit := p.runSingle(); exit {
				if finished {
					p.getIns()
				}
				break loop
			}

		}
	}
}
