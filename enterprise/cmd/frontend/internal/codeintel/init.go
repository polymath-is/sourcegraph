package codeintel

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/inconshreveable/log15"
	"github.com/opentracing/opentracing-go"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/enterprise"
	codeintelapi "github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/api"
	bundles "github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/bundles/client"
	codeintelgitserver "github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/gitserver"
	codeintelhttpapi "github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/httpapi"
	codeintelresolvers "github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/resolvers"
	codeintelgqlresolvers "github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/resolvers/graphql"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/store"
	"github.com/sourcegraph/sourcegraph/internal/db/basestore"
	"github.com/sourcegraph/sourcegraph/internal/db/dbconn"
	"github.com/sourcegraph/sourcegraph/internal/env"
	"github.com/sourcegraph/sourcegraph/internal/observation"
	"github.com/sourcegraph/sourcegraph/internal/trace"
)

var bundleManagerURL = env.Get("PRECISE_CODE_INTEL_BUNDLE_MANAGER_URL", "", "HTTP address for the internal LSIF bundle manager server.")
var rawHunkCacheSize = env.Get("PRECISE_CODE_INTEL_HUNK_CACHE_CAPACITY", "1000", "Maximum number of git diff hunk objects that can be loaded into the hunk cache at once.")
var indexerURL = env.Get("PRECISE_CODE_INTEL_INDEXER_URL", "", "HTTP address for the internal LSIF indexer server.")

func Init(ctx context.Context, enterpriseServices *enterprise.Services) error {
	if bundleManagerURL == "" {
		return fmt.Errorf("invalid value for PRECISE_CODE_INTEL_BUNDLE_MANAGER_URL: no value supplied")
	}

	if indexerURL == "" {
		return fmt.Errorf("invalid value for PRECISE_CODE_INTEL_INDEXER_URL: no value supplied")
	}

	hunkCacheSize, err := strconv.ParseInt(rawHunkCacheSize, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid int %q for PRECISE_CODE_INTEL_HUNK_CACHE_CAPACITY: %s", rawHunkCacheSize, err)
	}

	observationContext := &observation.Context{
		Logger:     log15.Root(),
		Tracer:     &trace.Tracer{Tracer: opentracing.GlobalTracer()},
		Registerer: prometheus.DefaultRegisterer,
	}

	store := store.NewObserved(store.NewWithHandle(basestore.NewHandleWithDB(dbconn.Global)), observationContext)
	bundleManagerClient := bundles.New(bundleManagerURL)
	api := codeintelapi.NewObserved(codeintelapi.New(store, bundleManagerClient, codeintelgitserver.DefaultClient), observationContext)
	hunkCache, err := codeintelresolvers.NewHunkCache(int(hunkCacheSize))
	if err != nil {
		return fmt.Errorf("failed to initialize hunk cache: %s", err)
	}

	enterpriseServices.CodeIntelResolver = codeintelgqlresolvers.NewResolver(codeintelresolvers.NewResolver(
		store,
		bundleManagerClient,
		api,
		hunkCache,
	))

	enterpriseServices.NewCodeIntelUploadHandler = func() http.Handler {
		return codeintelhttpapi.NewUploadHandler(store, bundleManagerClient, false)
	}

	//
	// TODO -rename redirect instead of proxy then, dumbo
	//

	enterpriseServices.NewCodeIntelInternalProxyHandler = func() http.Handler {
		base := mux.NewRouter().PathPrefix("/.internal-code-intel/").Subrouter()
		base.StrictSlash(true)

		//
		// TODO - token middleware here
		//

		base.Path("/git/{rest:.*}").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			location, err := url.Parse("http://localhost:3090/.internal/git/" + mux.Vars(r)["rest"])
			if err != nil {
				fmt.Printf("OH NO: %s\n", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			location.RawQuery = r.URL.RawQuery

			// TODO - call the handlers directly if possible
			w.Header().Set("Location", location.String())
			w.WriteHeader(http.StatusTemporaryRedirect)
		})

		base.Path("/index-queue/{rest:.*}").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			location, err := url.Parse(indexerURL + "/" + mux.Vars(r)["rest"])
			if err != nil {
				fmt.Printf("OH NO: %s\n", err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			location.RawQuery = r.URL.RawQuery

			// TODO - do a reverse proxy not a redirect here
			w.Header().Set("Location", location.String())
			w.WriteHeader(http.StatusTemporaryRedirect)
		})

		return base
	}

	return nil
}
