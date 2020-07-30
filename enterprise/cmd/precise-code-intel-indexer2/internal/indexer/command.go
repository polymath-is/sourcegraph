package indexer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// TODO - document
func command(ctx context.Context, command string, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("empty args")
	}

	cmd, stdout, stderr, err := makeCommand(ctx, command, args...)
	if err != nil {
		return err
	}

	wg := parallel(
		func() { processStream("stdout", stdout) },
		func() { processStream("stderr", stderr) },
	)

	if err := cmd.Start(); err != nil {
		return err
	}

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}

func makeCommand(ctx context.Context, command string, args ...string) (_ *exec.Cmd, stdout, stderr io.Reader, err error) {
	cmd := exec.CommandContext(ctx, command, args...)

	stdout, err = cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}

	stderr, err = cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, err
	}

	return cmd, stdout, stderr, nil
}

func parallel(funcs ...func()) *sync.WaitGroup {
	var wg sync.WaitGroup

	for _, f := range funcs {
		wg.Add(1)
		go func(f func()) {
			defer wg.Done()
			f()
		}(f)
	}

	return &wg
}

func processStream(prefix string, r io.Reader) {
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		fmt.Printf("%s: %s\n", prefix, scanner.Text())
	}
}
