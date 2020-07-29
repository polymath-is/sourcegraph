package indexer

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
)

func fetchRepository(ctx context.Context, repositoryID int, commit string) (string, error) {
	tempDir, err := ioutil.TempDir("", "")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tempDir)
		}
	}()

	if true {
		// TODO - implement
		return "", fmt.Errorf("unimplemented")
	}

	// archive, err := gitserverClient.Archive(ctx, store, repositoryID, commit)
	// if err != nil {
	// 	return "", errors.Wrap(err, "gitserver.Archive")
	// }

	// if err := tar.Extract(tempDir, archive); err != nil {
	// 	return "", errors.Wrap(err, "tar.Extract")
	// }

	return tempDir, nil
}
