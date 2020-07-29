package indexer

import (
	"context"
	"io/ioutil"
	"net/url"
	"os"
	"path"
)

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
