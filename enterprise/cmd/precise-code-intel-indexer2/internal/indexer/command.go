package indexer

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

func command(ctx context.Context, command string, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("empty args")
	}

	indexCmd := exec.CommandContext(ctx, command, args...)
	indexCmd.Stdout = pipe("stdout")
	indexCmd.Stderr = pipe("stderr")

	if err := indexCmd.Start(); err != nil {
		return err
	}

	if err := indexCmd.Wait(); err != nil {
		return err
	}

	// TODO - return logs for storage/surfacing
	return nil
}

const PipeBufferSize = 1024

func pipe(prefix string) io.WriteCloser {
	pr, pw := io.Pipe()

	go func() {
		// TODO - close pipe
		buf := make([]byte, PipeBufferSize)

		for {
			n, err := pr.Read(buf)
			if n > 0 {
				fmt.Printf("%s: %s\n", prefix, buf[:n])
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				// TODO - handle error reasonably
				panic(err.Error())
			}
		}
	}()

	return pw
}
