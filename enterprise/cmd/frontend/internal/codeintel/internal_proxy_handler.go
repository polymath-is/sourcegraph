package codeintel

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"

	"github.com/gorilla/mux"
	"github.com/inconshreveable/log15"
)

func makeInternalProxyHandlerFactory() (func() http.Handler, error) {
	// TODO - use envvar
	frontendOrigin, err := url.Parse(fmt.Sprintf("http://%s/.internal/git", "localhost:3090"))
	if err != nil {
		return nil, err // TODO - wrap error
	}

	indexerOrigin, err := url.Parse(indexerURL)
	if err != nil {
		return nil, err // TODO - wrap error
	}

	factory := func() http.Handler {
		base := mux.NewRouter().PathPrefix("/.internal-code-intel/").Subrouter()
		base.StrictSlash(true)

		base.Path("/git/{rest:.*}").Handler(internalProxyAuthTokenMiddleware(reverseProxy(frontendOrigin)))
		base.Path("/index-queue/{rest:.*}").Handler(internalProxyAuthTokenMiddleware(reverseProxy(indexerOrigin)))
		return base
	}

	return factory, nil
}

func internalProxyAuthTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, token, ok := r.BasicAuth()
		if !ok {
			w.Header().Add("WWW-Authenticate", `Basic realm="Sourcegraph"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if token != internalProxyAuthToken {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// TODO - metrics, trace
var client = http.DefaultClient

func reverseProxy(target *url.URL) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, err := makeProxyRequest(r, target)
		if err != nil {
			log15.Error("Failed to construct proxy request", "err", err)
			http.Error(w, fmt.Sprintf("failed to construct proxy request: %s", err), http.StatusInternalServerError)
			return
		}

		resp, err := client.Do(r)
		if err != nil {
			log15.Error("Failed to perform proxy request", "err", err)
			http.Error(w, fmt.Sprintf("failed to perform proxy request: %s", err), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		copyHeader(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}

func makeProxyRequest(r *http.Request, target *url.URL) (*http.Request, error) {
	getBody, err := makeReaderFactory(r.Body)
	if err != nil {
		return nil, err
	}

	u := r.URL
	u.Scheme = target.Scheme
	u.Host = target.Host
	u.Path = path.Join("/", target.Path, mux.Vars(r)["rest"])

	req, err := http.NewRequest(r.Method, u.String(), getBody())
	if err != nil {
		return nil, err
	}

	copyHeader(req.Header, r.Header)
	req.GetBody = func() (io.ReadCloser, error) { return getBody(), nil }
	return req, nil
}

func makeReaderFactory(r io.Reader) (func() io.ReadCloser, error) {
	content, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, err
	}

	factory := func() io.ReadCloser {
		return ioutil.NopCloser(bytes.NewReader(content))
	}

	return factory, nil
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
