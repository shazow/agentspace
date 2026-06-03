package manager

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type execRunner struct{}

const execOutputDrainDelay = 250 * time.Millisecond

func (r *execRunner) Start(spec processSpec) (process, error) {
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	if spec.ProcessGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Stdin = spec.Stdin
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr

	capture, err := configureExecOutputCapture(cmd, spec)
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		if capture != nil {
			capture.Close()
		}
		return nil, fmt.Errorf("start %s: %w", spec.Name, err)
	}
	if capture != nil {
		capture.Start()
	}

	return &execProcess{cmd: cmd, capture: capture}, nil
}

type execProcess struct {
	cmd     *exec.Cmd
	capture *execOutputCapture
}

func (p *execProcess) Wait() error {
	waitErr := p.cmd.Wait()
	if p.capture != nil {
		if captureErr := p.capture.WaitAfterProcessExit(execOutputDrainDelay); waitErr == nil {
			return captureErr
		}
	}
	return waitErr
}

func (p *execProcess) Signal(sig os.Signal) error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(sig)
}

func (p *execProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

func (p *execProcess) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func configureExecOutputCapture(cmd *exec.Cmd, spec processSpec) (*execOutputCapture, error) {
	if !spec.DebugOutput || spec.Logger == nil {
		return nil, nil
	}

	capture := &execOutputCapture{}
	if err := capture.configureStream(cmd, spec.Stdout, spec.Logger, spec.Name, "stdout", spec.CaptureFileOutput); err != nil {
		capture.Close()
		return nil, err
	}
	if err := capture.configureStream(cmd, spec.Stderr, spec.Logger, spec.Name, "stderr", spec.CaptureFileOutput); err != nil {
		capture.Close()
		return nil, err
	}
	if capture.empty() {
		return nil, nil
	}
	return capture, nil
}

type execOutputCapture struct {
	streams []execCapturedStream
	wg      sync.WaitGroup

	mu          sync.Mutex
	err         error
	active      int
	forcedClose bool
}

type execCapturedStream struct {
	reader      io.ReadCloser
	childWriter io.Closer
	writer      io.Writer
	logger      *execLineLogger
}

func (c *execOutputCapture) configureStream(cmd *exec.Cmd, w io.Writer, logger *slog.Logger, name, stream string, captureFileOutput bool) error {
	if _, ok := w.(*os.File); ok && !captureFileOutput {
		return nil
	}

	reader, childWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("capture %s %s: %w", name, stream, err)
	}
	level := slog.LevelDebug
	switch stream {
	case "stdout":
		cmd.Stdout = childWriter
	case "stderr":
		cmd.Stderr = childWriter
		level = slog.LevelInfo
	default:
		_ = reader.Close()
		_ = childWriter.Close()
		return fmt.Errorf("unknown stream %q", stream)
	}

	lineLogger := &execLineLogger{
		logger: logger.With("exec", name, "stream", stream),
		level:  level,
	}
	writer := io.Writer(lineLogger)
	if w != nil && shouldPreserveCapturedOutput(w, logger, level) {
		writer = io.MultiWriter(w, lineLogger)
	}

	c.active++
	c.streams = append(c.streams, execCapturedStream{
		reader:      reader,
		childWriter: childWriter,
		writer:      writer,
		logger:      lineLogger,
	})
	return nil
}

func shouldPreserveCapturedOutput(w io.Writer, logger *slog.Logger, level slog.Level) bool {
	if w == os.Stdout || w == os.Stderr {
		return !logger.Handler().Enabled(context.Background(), level)
	}
	return true
}

func (c *execOutputCapture) empty() bool {
	return c == nil || c.active == 0
}

func (c *execOutputCapture) Start() {
	for _, stream := range c.streams {
		stream := stream
		if err := stream.childWriter.Close(); err != nil {
			c.setError(err)
		}
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			_, copyErr := io.Copy(stream.writer, stream.reader)
			stream.logger.Flush()
			if copyErr != nil && !c.isForcedClosed() {
				c.setError(copyErr)
			}
		}()
	}
}

func (c *execOutputCapture) Wait() error {
	c.wg.Wait()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *execOutputCapture) WaitAfterProcessExit(delay time.Duration) error {
	done := make(chan error, 1)
	go func() {
		done <- c.Wait()
	}()

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		c.CloseReaders()
		return <-done
	}
}

func (c *execOutputCapture) Close() {
	for _, stream := range c.streams {
		_ = stream.childWriter.Close()
		_ = stream.reader.Close()
	}
}

func (c *execOutputCapture) CloseReaders() {
	c.mu.Lock()
	c.forcedClose = true
	c.mu.Unlock()
	for _, stream := range c.streams {
		_ = stream.reader.Close()
	}
}

func (c *execOutputCapture) setError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
	}
}

func (c *execOutputCapture) isForcedClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.forcedClose
}

type execLineLogger struct {
	mu     sync.Mutex
	logger *slog.Logger
	level  slog.Level
	buffer bytes.Buffer
}

func (l *execLineLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	remaining := p
	for len(remaining) > 0 {
		if index := bytes.IndexByte(remaining, '\n'); index >= 0 {
			l.buffer.Write(remaining[:index])
			l.logLocked()
			remaining = remaining[index+1:]
			continue
		}
		l.buffer.Write(remaining)
		break
	}
	return len(p), nil
}

func (l *execLineLogger) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buffer.Len() > 0 {
		l.logLocked()
	}
}

func (l *execLineLogger) logLocked() {
	line := l.buffer.String()
	l.buffer.Reset()
	l.logger.Log(context.Background(), l.level, line)
}
