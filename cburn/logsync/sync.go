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

//URLFetcher is url fetcher
type URLFetcher struct {
	ctx      context.Context
	done     chan error
	id       int
	urlEntry *URLEntry
	subTree  chan *URLEntry
	record   chan *URLEntry
	timeout  time.Duration
	resp     *http.Response
	err      error
}

//URLFetcher is Record fetcher
type RecordFetcher struct {
	ctx      context.Context
	done     chan error
	id       int
	urlEntry *URLEntry
	subUrls  *urlQueue
	files    map[string][]byte
	timeout  time.Duration
	resp     *http.Response
	err      error
}

type routineStat struct {
	routineLauched  int
	routineReturned int
	routineDone     chan error
	routineMax      int
}

//Controller is the global data
type Controller struct {
	ctx          context.Context
	wg           sync.WaitGroup
	subTree      chan *URLEntry
	record       chan *URLEntry
	urlCache     map[string]*URLEntry
	explorerStat *routineStat
	recordStat   *routineStat
}

func newRoutineStat(max int) *routineStat {
	return &routineStat{
		routineDone: make(chan error),
		routineMax:  max,
	}
}

//NewController returns new controller
func NewController(ctx context.Context) *Controller {
	return &Controller{
		ctx:          ctx,
		subTree:      make(chan *URLEntry, 5000),
		record:       make(chan *URLEntry, 1000),
		urlCache:     make(map[string]*URLEntry),
		explorerStat: newRoutineStat(1000),
		recordStat:   newRoutineStat(1000),
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
			case <-s.routineDone:
				s.routineReturned++
			default:
				if s.routineReturned >= s.routineLauched {
					break loop
				}
			}

		case <-s.routineDone:
			s.routineReturned++
			if s.routineReturned >= s.routineLauched {
				close(c.record)
				break loop
			}
		default:
			if s.routineLauched-s.routineReturned < s.routineMax {
				select {
				case urlEntry := <-c.subTree:
					if c.urlNewerThanCache(urlEntry) {
						fmt.Printf("Explorer[%d] : %s %s\n", s.routineLauched, urlEntry.url.String(), urlEntry.lastUpdateTime)
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
			case <-s.routineDone:
				s.routineReturned++
			default:
				if s.routineReturned >= s.routineLauched {
					break loop
				}
			}
		case <-s.routineDone:
			s.routineReturned++
		default:
			if s.routineLauched-s.routineReturned < s.routineMax {
				select {
				case record, ok := <-c.record:
					if ok {
						s.routineLauched++
						fmt.Printf("Processor[%d] : %s %s\n", s.routineLauched, record.url.String(), record.lastUpdateTime)
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
		id:       c.explorerStat.routineLauched,
		ctx:      c.ctx,
		done:     c.explorerStat.routineDone,
		urlEntry: urlEntry,
		timeout:  time.Second * 2,
		subTree:  c.subTree,
		record:   c.record,
	}
}

func (c *Controller) newRecordFetcher(urlEntry *URLEntry) *RecordFetcher {
	r := &RecordFetcher{
		id:       c.recordStat.routineLauched,
		ctx:      c.ctx,
		done:     c.recordStat.routineDone,
		urlEntry: urlEntry,
		subUrls:  newURLQueue(100),
		timeout:  time.Second * 2,
		files:    make(map[string][]byte),
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

func (p *URLFetcher) processPage() {

	var urls []*URLEntry
	ct := p.resp.Header.Get("Content-Type")
	if strings.Contains(ct, "UTF-8") == false {
		p.resp.Body.Close()
		return
	}
	z := html.NewTokenizer(p.resp.Body)

	func() {
		defer p.resp.Body.Close()
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
	p.done <- p.err
}

//Run starts the real work
func (p *URLFetcher) run() {
	defer p.onCompletion()

	client := &http.Client{}
	req, err := http.NewRequest("GET", p.urlEntry.url.String(), nil)
	if err != nil {
		//wrong format, don't have to reinsert the URL for retry
		p.err = err
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
		resp, err := client.Do(req)
		if err != nil {
			fmt.Println(err)
			p.genNewURLEntry(p.urlEntry)
			p.err = err
			return
		}
		p.resp = resp
		p.processPage()
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
									fmt.Println("Push :", u.String())
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
	p.done <- p.err
}

func (p *RecordFetcher) runSingle() bool {

	urlEntry := p.subUrls.Pop()
	if urlEntry == nil {
		fmt.Println("Finshed :", p.urlEntry.url.String())
		return true
	}

	if !strings.HasPrefix(urlEntry.url.String(), p.urlEntry.url.String()) {
		return false
	}

	client := &http.Client{}
	req, err := http.NewRequest("GET", urlEntry.url.String(), nil)

	if err != nil {
		p.err = err
		return false
	}
	//req.Header.Set("Connection", "close")
	ctx, cancel := context.WithTimeout(p.ctx, p.timeout)
	defer cancel()
	req.WithContext(ctx)

	select {
	case <-ctx.Done():
		return false
	default:
		resp, err := client.Do(req)
		if err != nil {
			p.subUrls.Push(urlEntry)
			p.err = err
			return false
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
	return false
}

func (p *RecordFetcher) run() {
	defer p.onCompletion()
loop:
	for {
		select {
		case <-p.ctx.Done():
			break loop
		default:
			if p.runSingle() {
				break loop
			}

		}
	}
}
