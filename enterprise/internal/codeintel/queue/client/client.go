package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"

	"github.com/opentracing-contrib/go-stdlib/nethttp"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/queue/types"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/store"
	"github.com/sourcegraph/sourcegraph/internal/metrics"
	"github.com/sourcegraph/sourcegraph/internal/trace/ot"
	"golang.org/x/net/context/ctxhttp"
)

type Client interface {
	Dequeue(ctx context.Context) (index store.Index, _ bool, _ error)
	Complete(ctx context.Context, indexID int, indexErr error) error
	Heartbeat(ctx context.Context) error
}

type client struct {
	indexerName string
	frontendURL string
	httpClient  *http.Client
	userAgent   string
}

var _ Client = &client{}

var requestMeter = metrics.NewRequestMeter("precise_code_intel_index_queue", "Total number of requests sent to precise code intel index queue.")

// defaultTransport is the default transport for precise code intel index queue clients.
// ot.Transport will propagate opentracing spans.
var defaultTransport = &ot.Transport{
	RoundTripper: requestMeter.Transport(&http.Transport{}, func(u *url.URL) string {
		return u.Path // TODO - determine names here
	}),
}

func NewClient(indexerName, frontendURL string) Client {
	return &client{
		indexerName: indexerName,
		httpClient:  &http.Client{Transport: defaultTransport},
		frontendURL: frontendURL,
		userAgent:   filepath.Base(os.Args[0]),
	}
}

func (c *client) Dequeue(ctx context.Context) (index store.Index, _ bool, _ error) {
	url, err := makeQueueURL(c.frontendURL, "dequeue")
	if err != nil {
		return store.Index{}, false, err
	}

	payload, err := marshalPayload(types.DequeueRequest{
		IndexerName: c.indexerName,
	})
	if err != nil {
		return store.Index{}, false, err
	}

	hasContent, body, err := c.do(ctx, "POST", url, payload)
	if err != nil {
		return store.Index{}, false, err
	}
	if !hasContent {
		return store.Index{}, false, nil
	}
	defer body.Close()

	if err := json.NewDecoder(body).Decode(&index); err != nil {
		return store.Index{}, false, err
	}

	return index, true, nil
}

func (c *client) Complete(ctx context.Context, indexID int, indexErr error) error {
	url, err := makeQueueURL(c.frontendURL, "complete")
	if err != nil {
		return err
	}

	rawPayload := types.CompleteRequest{
		IndexerName: c.indexerName,
		IndexID:     indexID,
	}
	if indexErr != nil {
		rawPayload.ErrorMessage = indexErr.Error()
	}

	payload, err := marshalPayload(rawPayload)
	if err != nil {
		return err
	}

	return c.doAndDrop(ctx, "POST", url, payload)
}

func (c *client) Heartbeat(ctx context.Context) error {
	url, err := makeQueueURL(c.frontendURL, "heartbeat")
	if err != nil {
		return err
	}

	payload, err := marshalPayload(types.HeartbeatRequest{
		IndexerName: c.indexerName,
	})
	if err != nil {
		return err
	}

	return c.doAndDrop(ctx, "POST", url, payload)
}

// doAndDrop performs an HTTP request to the frontend and ignores the body contents.
func (c *client) doAndDrop(ctx context.Context, method string, url *url.URL, payload io.Reader) error {
	hasContent, body, err := c.do(ctx, method, url, payload)
	if err != nil {
		return err
	}
	if hasContent {
		body.Close()
	}
	return nil
}

// do performs an HTTP request to the frontend and returns the body content as a reader.
func (c *client) do(ctx context.Context, method string, url *url.URL, body io.Reader) (hasContent bool, _ io.ReadCloser, err error) {
	span, ctx := ot.StartSpanFromContext(ctx, ".do")
	defer func() {
		if err != nil {
			ext.Error.Set(span, true)
			span.SetTag("err", err.Error())
		}
		span.Finish()
	}()

	req, err := http.NewRequest(method, url.String(), body)
	if err != nil {
		return false, nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req = req.WithContext(ctx)

	req, ht := nethttp.TraceRequest(
		span.Tracer(),
		req,
		nethttp.OperationName("Code Intel Index Queue Client"),
		nethttp.ClientTrace(false),
	)
	defer ht.Finish()

	resp, err := ctxhttp.Do(req.Context(), c.httpClient, req)
	if err != nil {
		return false, nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()

		if resp.StatusCode == http.StatusNoContent {
			return false, nil, nil
		}

		return false, nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	return true, resp.Body, nil
}

func makeQueueURL(baseURL, op string) (*url.URL, error) {
	base, err := url.Parse(fmt.Sprintf("http://%s", baseURL))
	if err != nil {
		return nil, err
	}

	return base.ResolveReference(&url.URL{Path: path.Join(".internal-code-intel", "index-queue", op)}), nil
}

func marshalPayload(payload interface{}) (io.Reader, error) {
	content, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(content), nil
}
