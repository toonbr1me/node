package singbox

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/pasarguard/node/backend"
	"github.com/pasarguard/node/common"
	"github.com/pasarguard/node/config"
	"github.com/shirou/gopsutil/v4/process"
)

type SingBox struct {
	config *Config
	cfg    *config.Config
	core   *Core

	mu sync.RWMutex
}

func NewSingBox(ctx context.Context, _ int, cfg *config.Config) (*SingBox, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}

	sbConfig, ok := ctx.Value(backend.ConfigKey{}).(*Config)
	if !ok || sbConfig == nil {
		return nil, errors.New("sing-box config has not been initialized")
	}

	users, _ := ctx.Value(backend.UsersKey{}).([]*common.User)
	sbConfig.syncUsers(users)

	executableAbsolutePath, err := filepath.Abs(cfg.SingBoxExecutablePath)
	if err != nil {
		return nil, err
	}

	assetsAbsolutePath, err := filepath.Abs(cfg.SingBoxAssetsPath)
	if err != nil {
		return nil, err
	}

	configAbsolutePath, err := filepath.Abs(cfg.GeneratedConfigPath)
	if err != nil {
		return nil, err
	}

	core, err := NewSingBoxCore(executableAbsolutePath, assetsAbsolutePath, configAbsolutePath, cfg.LogBufferSize)
	if err != nil {
		return nil, err
	}

	if err := core.Start(sbConfig, cfg.Debug); err != nil {
		return nil, err
	}

	sb := &SingBox{
		config: sbConfig,
		cfg:    cfg,
		core:   core,
	}

	log.Println("sing-box backend started, version:", sb.Version())
	return sb, nil
}

func (s *SingBox) Logs() chan string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.core.Logs()
}

func (s *SingBox) Version() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.core == nil {
		return ""
	}
	return s.core.Version()
}

func (s *SingBox) Started() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.core == nil {
		return false
	}
	return s.core.Started()
}

func (s *SingBox) Restart() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.core == nil {
		return errors.New("sing-box core is not initialized")
	}

	return s.core.Restart(s.config, s.cfg.Debug)
}

func (s *SingBox) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.core != nil {
		s.core.Stop()
		s.core = nil
	}
}

func (s *SingBox) SyncUser(_ context.Context, user *common.User) error {
	if user == nil {
		return errors.New("user payload is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.upsertUser(user)
	return s.core.Restart(s.config, s.cfg.Debug)
}

func (s *SingBox) SyncUsers(_ context.Context, users []*common.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.syncUsers(users)
	return s.core.Restart(s.config, s.cfg.Debug)
}

func (s *SingBox) GetSysStats(ctx context.Context) (*common.BackendStatsResponse, error) {
	s.mu.RLock()
	core := s.core
	s.mu.RUnlock()

	if core == nil {
		return nil, errors.New("sing-box core is not running")
	}

	pid := core.PID()
	if pid == 0 {
		return nil, errors.New("sing-box process is not available")
	}

	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		return nil, err
	}

	memInfo, err := proc.MemoryInfoWithContext(ctx)
	if err != nil {
		return nil, err
	}

	uptime := uint32(time.Since(core.StartTime()).Seconds())

	stats := &common.BackendStatsResponse{
		NumGoroutine: 0,
		NumGc:        0,
		Alloc:        memInfo.RSS,
		TotalAlloc:   memInfo.RSS,
		Sys:          memInfo.VMS,
		Mallocs:      0,
		Frees:        0,
		LiveObjects:  0,
		PauseTotalNs: 0,
		Uptime:       uptime,
	}
	return stats, nil
}

func (s *SingBox) GetStats(context.Context, *common.StatRequest) (*common.StatResponse, error) {
	return nil, errors.New("sing-box statistics API is not implemented")
}

func (s *SingBox) GetUserOnlineStats(context.Context, string) (*common.OnlineStatResponse, error) {
	return nil, errors.New("sing-box online statistics are not implemented")
}

func (s *SingBox) GetUserOnlineIpListStats(context.Context, string) (*common.StatsOnlineIpListResponse, error) {
	return nil, errors.New("sing-box IP statistics are not implemented")
}
