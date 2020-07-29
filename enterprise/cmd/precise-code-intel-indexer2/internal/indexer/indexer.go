package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"sync"
	"time"

	"github.com/efritz/glock"
	"github.com/inconshreveable/log15"
	"github.com/pkg/errors"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/store"
)

type Indexer struct {
	options  IndexerOptions
	clock    glock.Clock
	ctx      context.Context // root context passed to commands
	cancel   func()          // cancels the root context
	wg       sync.WaitGroup  // tracks active background goroutines
	finished chan struct{}   // signals that Start has finished
}

type IndexerOptions struct {
	FrontendURL string
	Interval    time.Duration
	Metrics     IndexerMetrics
}

func NewIndexer(ctx context.Context, options IndexerOptions) *Indexer {
	return newIndexer(ctx, options, glock.NewRealClock())
}

func newIndexer(ctx context.Context, options IndexerOptions, clock glock.Clock) *Indexer {
	ctx, cancel := context.WithCancel(ctx)

	return &Indexer{
		options:  options,
		clock:    clock,
		ctx:      ctx,
		cancel:   cancel,
		finished: make(chan struct{}),
	}
}

// Start begins polling for work from the API and indexing repositories.
func (i *Indexer) Start() {
	defer close(i.finished)

	// TODO - configure, or otherwise proxy from frontend
	baseURL := "http://localhost:3189"

	//
	// TODO - need to add heartbeat
	//

loop:
	for {
		index, dequeued, err := dequeue(baseURL)
		if err != nil {
			log15.Error("failed to poll index job", "err", err)
		}

		delay := i.options.Interval
		if dequeued {
			delay = 0

			indexErr := process(i.ctx, i.options.FrontendURL, index)
			if err := complete(baseURL, index.ID, indexErr); err != nil {
				log15.Error("OOPS", "err", err)
			}
		} else {
			fmt.Printf("NOTHING TO PROCESS\n")
		}

		select {
		case <-i.clock.After(delay):
		case <-i.ctx.Done():
			break loop
		}
	}

	i.wg.Wait()
}

// Stop will cause the indexer loop to exit after the current iteration. This is done by canceling the
// context passed to the subprocess functions (which may cause the currently processing unit of work
// to fail). This method blocks until all background goroutines have exited.
func (i *Indexer) Stop() {
	i.cancel()
	<-i.finished
}

// TODO - configure
const IndexerName = "foobar!!"

func dequeue(baseURL string) (index store.Index, _ bool, _ error) {
	req, err := http.NewRequest("POST", fmt.Sprintf("%s/dequeue?indexerName=%s", baseURL, IndexerName), nil)
	if err != nil {
		return store.Index{}, false, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return store.Index{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return store.Index{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return store.Index{}, false, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		return store.Index{}, false, err
	}

	return index, true, nil
}

func complete(baseURL string, indexID int, err error) error {
	var errorMessage string
	if err != nil {
		errorMessage = fmt.Sprintf("&errorMessage=%s", err.Error())
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/complete?indexerName=%s&indexId=%d%s", baseURL, IndexerName, indexID, errorMessage), nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	return nil
}

func process(ctx context.Context, baseURL string, index store.Index) error {
	repoDir, err := fetchRepository(ctx, baseURL, index.RepositoryID, index.RepositoryName, index.Commit)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(repoDir)
	}()

	indexCommand := []string{
		"run",
		"--rm",
		"-v", "`pwd`:/data",
		"-w", "/data",
		"sourcegraph/lsif-go:latest",
		"bash", "-c", fmt.Sprintf("'lsif-go && src lsif upload'"),
	}

	if err := command(repoDir, "docker", indexCommand...); err != nil {
		return errors.Wrap(err, "failed to index repository")
	}

	return nil
}

func fetchRepository(ctx context.Context, frontendURL string, repositoryID int, repositoryName string, commit string) (string, error) {
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tempDir)
		}
	}()

	frontendX, err := url.Parse(frontendURL)
	if err != nil {
		return "", err
	}

	if err := command(tempDir, "git", "init", "--bare"); err != nil {
		return "", err
	}

	cloneURL := frontendX.ResolveReference(&url.URL{
		Path: path.Join("/.internal/git", repositoryName),
	})

	fetchArgs := []string{
		"-C", tempDir,
		"-c", "protocol.version=2",
		"fetch",
		// "--depth=1",
		cloneURL.String(),
		commit,
	}

	if err := command(tempDir, "git", fetchArgs...); err != nil {
		return "", err
	}

	return tempDir, nil
}

func command(dir, command string, args ...string) error {
	indexCmd := exec.Command(command, args...)
	indexCmd.Dir = dir

	if output, err := indexCmd.CombinedOutput(); err != nil {
		return errors.Wrap(err, fmt.Sprintf("command failed: %s\n", output))
	}

	return nil
}
