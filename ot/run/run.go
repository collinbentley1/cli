package run

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

type Spec struct {
	Program string
	Args    []string
	Dir     string
	Env     map[string]string
}

func (s Spec) String() string {
	parts := append([]string{s.Program}, s.Args...)
	return strings.Join(parts, " ")
}

func RunInteractive(ctx context.Context, spec Spec) error {
	cmd := exec.CommandContext(ctx, spec.Program, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = mergedEnv(spec.Env)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func RunSilent(ctx context.Context, spec Spec) error {
	cmd := exec.CommandContext(ctx, spec.Program, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = mergedEnv(spec.Env)
	return cmd.Run()
}

func RunCapture(ctx context.Context, spec Spec) (string, error) {
	cmd := exec.CommandContext(ctx, spec.Program, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = mergedEnv(spec.Env)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

type LineCallback func(line string)

func RunTee(ctx context.Context, spec Spec, maxCapture int) (string, error) {
	return RunTeeWithCallback(ctx, spec, maxCapture, nil)
}

func RunTeeWithCallback(ctx context.Context, spec Spec, maxCapture int, onLine LineCallback) (string, error) {
	cmd := exec.CommandContext(ctx, spec.Program, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = mergedEnv(spec.Env)
	cmd.Stdin = os.Stdin

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("stderr pipe: %w", err)
	}

	var buf bytes.Buffer
	var mu sync.Mutex
	appendCapture := func(p []byte) {
		if maxCapture <= 0 {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		remaining := maxCapture - buf.Len()
		if remaining <= 0 {
			return
		}
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = buf.Write(p)
	}

	tee := func(r io.Reader, w io.Writer) {
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				_, _ = w.Write(line)
				appendCapture(line)
				if onLine != nil {
					onLine(string(line))
				}
			}
			if err != nil {
				return
			}
		}
	}

	if err := cmd.Start(); err != nil {
		return "", err
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); tee(stdout, os.Stdout) }()
	go func() { defer wg.Done(); tee(stderr, os.Stderr) }()

	err = cmd.Wait()
	wg.Wait()

	return buf.String(), err
}

func mergedEnv(extra map[string]string) []string {
	base := os.Environ()
	if len(extra) == 0 {
		return base
	}
	out := make([]string, len(base))
	copy(out, base)
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}

func RunInteractiveWithSignals(ctx context.Context, spec Spec) error {
	cmd := exec.CommandContext(ctx, spec.Program, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = mergedEnv(spec.Env)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, sig.(syscall.Signal))
			}
		}
	}()
	// Never close a signal.Notify channel: a signal delivered between the
	// close and signal.Stop would make os/signal's delivery goroutine panic
	// with "send on closed channel". After Stop, the relay goroutine above
	// blocks on the unreferenced channel and both are garbage collected.
	defer signal.Stop(sigCh)

	return cmd.Wait()
}

func RunInteractiveDetached(ctx context.Context, spec Spec) error {
	cmd := exec.CommandContext(ctx, spec.Program, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = mergedEnv(spec.Env)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}

	return cmd.Process.Release()
}
