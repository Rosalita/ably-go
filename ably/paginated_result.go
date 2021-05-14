package ably

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/ably/ably-go/ably/proto"
)

type Direction string

const (
	Backwards Direction = "backwards"
	Forwards  Direction = "forwards"
)

type paginatedRequestNew struct {
	path   string
	params url.Values

	query queryFunc
}

func (r *REST) newPaginatedRequest(path string, params url.Values) paginatedRequestNew {
	return paginatedRequestNew{
		path:   path,
		params: params,

		query: query(r.get),
	}
}

// PaginatedResultNew is a generic iterator for PaginatedResult pagination.
// Items decoding is delegated to type-specific wrappers.
//
// See "Paginated results" section in the package-level documentation.
type PaginatedResultNew struct {
	basePath  string
	nextLink  string
	firstLink string
	res       *http.Response
	err       error

	query queryFunc
	first bool
}

// load loads the first page of results. Must be called from the type-specific
// wrapper Pages method that creates the PaginatedResult object.
func (p *PaginatedResultNew) load(ctx context.Context, r paginatedRequestNew) error {
	p.basePath = path.Dir(r.path)
	p.firstLink = (&url.URL{
		Path:     r.path,
		RawQuery: r.params.Encode(),
	}).String()
	p.query = r.query
	return p.First(ctx)
}

// loadItems loads the first page of results and returns a next function. Must
// be called from the type-specific wrapper Items method that creates the
// PaginatedItems object.
//
// The returned next function must be called from the wrapper's Next method, and
// returns the index of the object that should be returned by the Item method,
// previously loading the next page if necessary.
//
// pageDecoder will be called each time a new page is retrieved under the hood.
// It should return a destination object on which the page of results will be
// decoded, and a pageLength function that, when called after the page has been
// decoded, must return the length of the page.
func (p *PaginatedResultNew) loadItems(
	ctx context.Context,
	r paginatedRequestNew,
	pageDecoder func() (page interface{}, pageLength func() int),
) (
	next func(context.Context) (int, bool),
	err error,
) {
	err = p.load(ctx, r)
	if err != nil {
		return nil, err
	}

	var page interface{}
	var pageLen int
	nextItem := 0

	return func(ctx context.Context) (int, bool) {
		if nextItem == 0 {
			var getLen func() int
			page, getLen = pageDecoder()
			hasNext := p.next(ctx, &page)
			if !hasNext {
				return 0, false
			}
			pageLen = getLen()
			if pageLen == 0 { // compatible with hasNext if first page is empty
				return 0, false
			}
		}

		idx := nextItem
		nextItem = (nextItem + 1) % pageLen
		return idx, true
	}, nil
}

func (p *PaginatedResultNew) goTo(ctx context.Context, link string) error {
	var err error
	p.res, err = p.query(ctx, link)
	if err != nil {
		return err
	}
	p.nextLink = ""
	for _, rawLink := range p.res.Header["Link"] {
		m := relLinkRegexp.FindStringSubmatch(rawLink)
		if len(m) == 0 {
			continue
		}
		relPath, rel := m[1], m[2]
		path := path.Join(p.basePath, relPath)
		switch rel {
		case "first":
			p.firstLink = path
		case "next":
			p.nextLink = path
		}
	}
	return nil
}

// next loads the next page of items, if there is one. It returns whether a page
// was successfully loaded or not; after it returns false, Err should be
// called to check for any errors.
//
// Items can then be inspected with the type-specific Items method.
//
// For items iterators, use the next function returned by loadItems instead.
func (p *PaginatedResultNew) next(ctx context.Context, into interface{}) bool {
	if !p.first {
		if p.nextLink == "" {
			return false
		}
		p.err = p.goTo(ctx, p.nextLink)
		if p.err != nil {
			return false
		}
	}
	p.first = false

	p.err = decodeResp(p.res, into)
	return p.err == nil
}

// First loads the first page of items. Next should be called before inspecting
// the Items.
func (p *PaginatedResultNew) First(ctx context.Context) error {
	p.first = true
	return p.goTo(ctx, p.firstLink)
}

// Err returns the error that caused Next to fail, if there was one.
func (p *PaginatedResultNew) Err() error {
	return p.err
}

// relLinkRegexp is the regexp that matches our pagination format
var relLinkRegexp = regexp.MustCompile(`<(?P<url>[^>]+)>; rel="(?P<rel>[^"]+)"`)

type errInvalidType struct {
	typ reflect.Type
}

func (err errInvalidType) Error() string {
	return "requested value of incompatible type: " + err.typ.String()
}

// queryFunc queries the given URL and gives non-nil HTTP response if no error
// occurred.
type queryFunc func(ctx context.Context, url string) (*http.Response, error)

// PaginatedResult represents a single page coming back from the REST API.
// Any call to create a new page will generate a new instance.
type PaginatedResult struct {
	path     string
	headers  map[string]string
	links    []string
	items    []interface{}
	typItems interface{}
	opts     *proto.ChannelOptions
	req      paginatedRequest

	statusCode   int
	success      bool
	errorCode    int
	errorMessage string
	respHeaders  http.Header
}

type paginatedRequest struct {
	typ       reflect.Type
	path      string
	params    *PaginateParams
	query     queryFunc
	logger    *LoggerOptions
	respCheck func(*http.Response) error
	decoder   func(*proto.ChannelOptions, reflect.Type, *http.Response) (interface{}, error)
}

func decodePaginatedResult(opts *proto.ChannelOptions, typ reflect.Type, resp *http.Response) (interface{}, error) {
	v := reflect.New(typ)
	if err := decodeResp(resp, v.Interface()); err != nil {
		return nil, err
	}
	return v.Elem().Interface(), nil
}

func newPaginatedResult(ctx context.Context, opts *proto.ChannelOptions, req paginatedRequest) (*PaginatedResult, error) {
	if req.decoder == nil {
		req.decoder = decodePaginatedResult
	}
	p := &PaginatedResult{
		opts: opts,
		req:  req,
	}
	builtPath, err := p.buildPaginatedPath(req.path, req.params)
	if err != nil {
		return nil, err
	}
	resp, err := p.req.query(ctx, builtPath)
	if err != nil {
		return nil, err
	}
	if err = req.respCheck(resp); err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if p.respHeaders == nil {
		p.respHeaders = make(http.Header)
	}
	p.statusCode = resp.StatusCode
	p.success = 200 <= p.statusCode && p.statusCode < 300
	copyHeader(p.respHeaders, resp.Header)
	if h := p.respHeaders.Get(proto.AblyErrorCodeHeader); h != "" {
		i, err := strconv.Atoi(h)
		if err != nil {
			return nil, err
		}
		p.errorCode = i
	} else if !p.success {
		return nil, malformedPaginatedResponseError(resp)
	}
	if h := p.respHeaders.Get(proto.AblyErrormessageHeader); h != "" {
		p.errorMessage = h
	} else if !p.success {
		return nil, malformedPaginatedResponseError(resp)
	}
	p.path = builtPath
	p.links = resp.Header["Link"]
	if p.statusCode != 204 { // Don't try to decode when there is no content.
		v, err := p.req.decoder(opts, p.req.typ, resp)
		if err != nil {
			return nil, err
		}
		p.typItems = v
	}
	return p, nil
}

func malformedPaginatedResponseError(resp *http.Response) error {
	body := make([]byte, 200)
	n, err := io.ReadFull(resp.Body, body)
	body = body[:n]
	msg := fmt.Sprintf("invalid PaginatedResult HTTP response; status: %d; body (first %d bytes): %q", resp.StatusCode, len(body), body)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s; body read error: %w", msg, err)
	}
	return errors.New(msg)
}

func copyHeader(dest, src http.Header) {
	for k, v := range src {
		d := make([]string, len(v))
		copy(d, v)
		dest[k] = v
	}
}

// Next returns the path to the next page as found in the response headers.
// The response headers from the REST API contains a relative link to the next result.
// (Link: <./path>; rel="next").
//
// If there is no next link, both return values are nil.
func (p *PaginatedResult) Next(ctx context.Context) (*PaginatedResult, error) {
	nextPath, ok := p.paginationHeaders()["next"]
	if !ok {
		return nil, nil
	}
	nextPage := p.buildPath(p.path, nextPath)
	req := p.req
	req.path = nextPage
	req.params = nil
	return newPaginatedResult(ctx, p.opts, req)
}

// Items gives a slice of results of the current page.
func (p *PaginatedResult) Items() []interface{} {
	if p.items == nil {
		v := reflect.ValueOf(p.typItems)
		if v.Kind() == reflect.Slice {
			p.items = make([]interface{}, v.Len())
			for i := range p.items {
				p.items[i] = v.Index(i).Interface()
			}
		} else {
			p.items = []interface{}{p.typItems}
		}
	}
	return p.items
}

// PresenceMessages gives a slice of presence messages for the current path.
// The method panics if the underlying paginated result is not a presence message.
func (p *PaginatedResult) PresenceMessages() []*PresenceMessage {
	items, ok := p.typItems.([]*proto.PresenceMessage)
	if !ok {
		panic(errInvalidType{typ: p.req.typ})
	}
	return items
}

type Stats = proto.Stats
type StatsMessageTypes = proto.MessageTypes
type StatsMessageCount = proto.MessageCount
type StatsMessageTraffic = proto.MessageTraffic
type StatsConnectionTypes = proto.ConnectionTypes
type StatsResourceCount = proto.ResourceCount
type StatsRequestCount = proto.RequestCount
type StatsPushStats = proto.PushStats
type StatsXchgMessages = proto.XchgMessages
type PushStats = proto.PushStats

func (c *PaginatedResult) buildPaginatedPath(path string, params *PaginateParams) (string, error) {
	if params == nil {
		return path, nil
	}
	values := &url.Values{}
	err := params.EncodeValues(values)
	if err != nil {
		return "", newError(50000, err)
	}
	queryString := values.Encode()
	if len(queryString) > 0 {
		return path + "?" + queryString, nil
	}
	return path, nil
}

// buildPath finds the absolute path based on the path parameter and the new relative path.
func (p *PaginatedResult) buildPath(origPath string, newRelativePath string) string {
	if i := strings.IndexRune(origPath, '?'); i != -1 {
		origPath = origPath[:i]
	}
	return path.Join(path.Dir(origPath), newRelativePath)
}

func (p *PaginatedResult) paginationHeaders() map[string]string {
	if p.headers == nil {
		p.headers = make(map[string]string)
		for _, link := range p.links {
			if result := relLinkRegexp.FindStringSubmatch(link); result != nil {
				p.addMatch(result)
			}
		}
	}
	return p.headers
}

func (p *PaginatedResult) addMatch(matches []string) {
	matchingNames := relLinkRegexp.SubexpNames()
	matchMap := map[string]string{}
	for i, value := range matches {
		matchMap[matchingNames[i]] = value
	}
	p.headers[matchMap["rel"]] = matchMap["url"]
}
