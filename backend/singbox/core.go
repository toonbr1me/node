package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Core struct {
	executablePath string
	assetsPath     string
	configDir      string
	process        *exec.Cmd
	restarting     bool
	logsChan       chan string
	version        string
	cancelFunc     context.CancelFunc
	startTime      time.Time
	mu             sync.Mutex
}

func NewSingBoxCore(executablePath, assetsPath, configDir string, logBufferSize int) (*Core, error) {
	core := &Core{
		executablePath: executablePath,
		assetsPath:     assetsPath,
		configDir:      configDir,
		logsChan:       make(chan string, logBufferSize),
	}

	version, err := core.refreshVersion()
	if err != nil {
		return nil, err
	}
	core.version = version

	return core, nil
}

func (c *Core) refreshVersion() (string, error) {
	cmd := exec.Command(c.executablePath, "version")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}

	output := buf.String()
	re := regexp.MustCompile(`(\d+\.\d+\.\d+)`)
	if match := re.FindString(output); match != "" {
		return match, nil
	}
	return strings.TrimSpace(output), nil
}

func (c *Core) Version() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

func (c *Core) Started() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.process == nil || c.process.Process == nil {
		return false
	}
	return c.process.ProcessState == nil
}

func (c *Core) Logs() chan string {
	return c.logsChan
}

func (c *Core) Start(cfg *Config, _ bool) error {
	bytesConfig, err := cfg.ToBytes()
	if err != nil {
		return err
	}

	if err := c.writeConfigFile(bytesConfig); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.process != nil && c.process.Process != nil {
		return fmt.Errorf("sing-box is already running")
	}

	cmd := exec.Command(c.executablePath, "run", "-c", filepath.Join(c.configDir, "sing-box.json"))
	cmd.Env = append(os.Environ(), "SING_BOX_LOCATION_ASSET="+c.assetsPath)
	setProcAttributes(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancelFunc = cancel
	c.process = cmd
	c.startTime = time.Now()

	go c.captureProcessLogs(ctx, stdout)
	go c.captureProcessLogs(ctx, stderr)
	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

func (c *Core) Restart(cfg *Config, debug bool) error {
	c.mu.Lock()
	if c.restarting {
		c.mu.Unlock()
		return fmt.Errorf("sing-box is already restarting")
	}
	c.restarting = true
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.restarting = false
		c.mu.Unlock()
	}()

	c.Stop()
	return c.Start(cfg, debug)
}

func (c *Core) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancelFunc != nil {
		c.cancelFunc()
		c.cancelFunc = nil
	}

	if c.process == nil || c.process.Process == nil {
		return
	}

	done := make(chan struct{})
	go func() {
		_ = c.process.Wait()
		close(done)
	}()

	_ = c.process.Process.Kill()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Printf("sing-box process %d did not stop within timeout", c.process.Process.Pid)
	}

	c.process = nil
}

func (c *Core) PID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.process == nil || c.process.Process == nil {
		return 0
	}
	return c.process.Process.Pid
}

func (c *Core) StartTime() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startTime
}

func (c *Core) writeConfigFile(config []byte) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, config, "", "    "); err != nil {
		// fallback to raw config if indent fails
		pretty = *bytes.NewBuffer(config)
	}

	if err := os.MkdirAll(c.configDir, 0o755); err != nil {
		return err
	}

	configFile := filepath.Join(c.configDir, "sing-box.json")
	return os.WriteFile(configFile, pretty.Bytes(), 0o600)
}

func (c *Core) captureProcessLogs(ctx context.Context, reader io.Reader) {
	captureLogs(ctx, reader, c.logsChan)
}
