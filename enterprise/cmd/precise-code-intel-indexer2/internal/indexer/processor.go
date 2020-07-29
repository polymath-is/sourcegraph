package indexer

import (
	"context"
	"fmt"
	"os"

	"github.com/pkg/errors"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/store"
)

type Processor interface {
	Process(ctx context.Context, index store.Index) error
}

type processor struct {
	frontendURL string
}

func (p *processor) Process(ctx context.Context, index store.Index) error {
	repoDir, err := fetchRepository(ctx, index.RepositoryID, index.Commit)
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
