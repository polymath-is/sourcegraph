package enterprise

import (
	"fmt"
	"net/http"

	"github.com/sourcegraph/sourcegraph/cmd/frontend/graphqlbackend"
)

// Services is a bag of HTTP handlers and factory functions that are registered by the
// enterprise frontend setup hook.
type Services struct {
	GithubWebhook                    http.Handler
	BitbucketServerWebhook           http.Handler
	NewCodeIntelUploadHandler        NewCodeIntelUploadHandler
	NewCodeIntelInternalProxyHandler NewCodeIntelInternalProxyHandler
	AuthzResolver                    graphqlbackend.AuthzResolver
	CampaignsResolver                graphqlbackend.CampaignsResolver
	CodeIntelResolver                graphqlbackend.CodeIntelResolver
}

// NewCodeIntelUploadHandler creates a new handler for the LSIF upload endpoint.
type NewCodeIntelUploadHandler func() http.Handler

// NewCodeIntelInternalProxyHandler creates a new proxy handler for internal code intel routes
// accessible from the precise-code-intel-indexer (deployed separately from the k8s cluster).
type NewCodeIntelInternalProxyHandler func() http.Handler

// DefaultServices creates a new Services value that has default implementations for all services.
func DefaultServices() Services {
	return Services{
		GithubWebhook:                    makeNotFoundHandler("github webhook"),
		BitbucketServerWebhook:           makeNotFoundHandler("bitbucket server webhook"),
		NewCodeIntelUploadHandler:        func() http.Handler { return makeNotFoundHandler("code intel upload") },
		NewCodeIntelInternalProxyHandler: func() http.Handler { return makeNotFoundHandler("code intel internal proxy") },
		AuthzResolver:                    graphqlbackend.DefaultAuthzResolver,
		CampaignsResolver:                graphqlbackend.DefaultCampaignsResolver,
		CodeIntelResolver:                graphqlbackend.DefaultCodeIntelResolver,
	}
}

// makeNotFoundHandler returns an HTTP handler that respond 404 for all requests.
func makeNotFoundHandler(handlerName string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(fmt.Sprintf("%s is only available in enterprise", handlerName)))
	})
}
