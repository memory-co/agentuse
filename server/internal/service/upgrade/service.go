// Package upgrade owns Server upgrade attempts, prepared artifacts and version history.
package upgrade

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/version"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/database"
	"github.com/memory-co/agentuse/server/internal/notification"
	"gorm.io/gorm"
)

var ErrUnavailable = errors.New("当前运行方式未启用 Server 自升级")

type Config struct {
	Context                                      context.Context
	Root, Executable, DistDir, Instance, Version string
	APIURL, DownloadURL                          string
	HTTPClient                                   *http.Client
	Restart                                      func(context.Context, Restart) error
	Now                                          func() time.Time
	Log                                          *log.Logger
	Changes                                      *notification.Broker
}

type Service struct {
	DB          *gorm.DB
	config      Config
	instanceKey string
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	workers     sync.WaitGroup
	closed      bool
}

func New(db *gorm.DB, config Config) *Service {
	if config.Context == nil {
		config.Context = context.Background()
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Log == nil {
		config.Log = log.Default()
	}
	if config.Version == "" {
		config.Version = version.Version
	}
	if config.APIURL == "" {
		config.APIURL = githubAPIURL
	}
	if config.DownloadURL == "" {
		config.DownloadURL = githubDownloadURL
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Minute}
	}
	ctx, cancel := context.WithCancel(config.Context)
	return &Service{DB: db, config: config, instanceKey: fmt.Sprintf("%x", sha256.Sum256([]byte(config.Instance+"|"+config.Executable))), ctx: ctx, cancel: cancel}
}

func (s *Service) Supported() bool {
	_, stable := versionNumbers(s.config.Version)
	return stable && (runtime.GOOS == "linux" || runtime.GOOS == "darwin") && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64") && s.config.Executable != "" && s.config.Root != "" && s.config.DistDir != "" && s.config.Restart != nil
}

func view(row database.ServerUpgrade) agentuse.ServerUpgrade {
	return agentuse.ServerUpgrade{ID: row.ID, FromVersion: row.FromVersion, TargetVersion: row.TargetVersion, Status: row.Status, ReleaseURL: string(row.ReleaseURL), DownloadedBytes: row.DownloadedBytes, TotalBytes: row.TotalBytes, Error: string(row.Error), RequestedBy: row.RequestedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, CompletedAt: row.CompletedAt}
}

func (s *Service) Get(ctx context.Context, id string) (agentuse.ServerUpgrade, error) {
	var row database.ServerUpgrade
	err := s.DB.WithContext(ctx).Where("instance_key=? AND id=?", s.instanceKey, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = database.ErrNotFound
	}
	return view(row), err
}

func (s *Service) active(ctx context.Context) (*agentuse.ServerUpgrade, error) {
	var row database.ServerUpgrade
	err := s.DB.WithContext(ctx).Where("instance_key=? AND active_marker=1", s.instanceKey).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v := view(row)
	return &v, nil
}

func (s *Service) State(ctx context.Context, cursor string, limit int) (agentuse.ServerUpgradeState, error) {
	out := agentuse.ServerUpgradeState{CurrentVersion: s.config.Version, Platform: runtime.GOOS + "/" + runtime.GOARCH, Supported: s.Supported(), Upgrades: []agentuse.ServerUpgrade{}}
	if !out.Supported {
		out.Reason = ErrUnavailable.Error()
		if _, stable := versionNumbers(s.config.Version); !stable {
			out.Reason = "开发构建无法确定升级顺序，请先安装正式版本"
		}
	}
	var err error
	if out.Active, err = s.active(ctx); err != nil {
		return out, err
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	query := s.DB.WithContext(ctx).Where("instance_key=?", s.instanceKey)
	if cursor != "" {
		var last database.ServerUpgrade
		if err = s.DB.WithContext(ctx).Where("instance_key=? AND id=?", s.instanceKey, cursor).First(&last).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				err = database.ErrNotFound
			}
			return out, err
		}
		query = query.Where("seq<?", last.Seq)
	}
	var rows []database.ServerUpgrade
	if err = query.Order("seq DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return out, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		id := rows[len(rows)-1].ID
		out.NextCursor = &id
	}
	for _, row := range rows {
		out.Upgrades = append(out.Upgrades, view(row))
	}
	return out, nil
}

// Start records acceptance before doing network or filesystem work. The worker
// belongs to the Server lifecycle, not to the initiating HTTP request.
func (s *Service) Start(ctx context.Context, userID string) (agentuse.ServerUpgrade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Supported() || s.closed {
		return agentuse.ServerUpgrade{}, ErrUnavailable
	}
	if current, err := s.active(ctx); err != nil {
		return agentuse.ServerUpgrade{}, err
	} else if current != nil {
		return *current, nil
	}
	now := s.config.Now().Unix()
	marker := int16(1)
	row := database.ServerUpgrade{ID: uuid.NewString(), InstanceKey: s.instanceKey, OwnerPID: os.Getpid(), ActiveMarker: &marker, FromVersion: s.config.Version, Status: "checking", RequestedBy: userID, CreatedAt: now, UpdatedAt: now}
	// Once enqueueing begins, a disappearing HTTP connection must not leave a
	// committed record without its worker because ExecContext was cancelled.
	acceptCtx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	if err := s.DB.WithContext(acceptCtx).Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			if active, e := s.active(ctx); e == nil && active != nil {
				return *active, nil
			}
		}
		return agentuse.ServerUpgrade{}, err
	}
	s.notify(row.ID)
	s.workers.Add(1)
	go s.execute(row.ID)
	return view(row), nil
}

func (s *Service) update(id string, changes map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	changes["updated_at"] = s.config.Now().Unix()
	err := s.DB.WithContext(ctx).Model(&database.ServerUpgrade{}).Where("instance_key=? AND id=?", s.instanceKey, id).Updates(changes).Error
	if err == nil {
		s.notify(id)
	}
	return err
}

func (s *Service) finish(id, status string, failure error) error {
	message := ""
	if failure != nil {
		message = failure.Error()
	}
	return s.update(id, map[string]any{"status": status, "error": message, "active_marker": nil, "completed_at": s.config.Now().Unix()})
}
func (s *Service) notify(id string) {
	s.config.Changes.Publish(notification.Change{Resource: "upgrades", ID: id})
}

func (s *Service) execute(id string) {
	defer s.workers.Done()
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Minute)
	defer cancel()
	err := s.perform(ctx, id)
	if err == nil {
		return
	}
	plan := Restart{ID: id, Directory: filepath.Join(s.config.Root, id), Executable: s.config.Executable}
	if rollbackErr := plan.Rollback(); rollbackErr != nil {
		err = fmt.Errorf("%v; 恢复原文件失败: %w", err, rollbackErr)
	}
	if writeErr := s.finish(id, "failed", err); writeErr != nil {
		s.config.Log.Printf("upgrade %s failed: %v; record: %v", id, err, writeErr)
	}
}

func (s *Service) perform(ctx context.Context, id string) error {
	release, err := s.fetchRelease(ctx)
	if err != nil {
		return err
	}
	if err = s.update(id, map[string]any{"target_version": release.Tag, "release_url": release.URL}); err != nil {
		return err
	}
	if !newer(release.Tag, s.config.Version) {
		return s.finish(id, "unchanged", nil)
	}
	plan, err := s.download(ctx, id, release)
	if err != nil {
		return err
	}
	if err = s.update(id, map[string]any{"status": "prepared"}); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = plan.Prepare(s.config.DistDir); err != nil {
		return err
	}
	if err = s.update(id, map[string]any{"status": "installing"}); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = plan.Apply(); err != nil {
		return err
	}
	if err = s.update(id, map[string]any{"status": "restarting"}); err != nil {
		return err
	}
	return s.config.Restart(ctx, plan)
}

// Recover runs after binding and initialization, before accepting requests. A
// restarted binary confirms success; interrupted attempts retain a failure.
func (s *Service) Recover(ctx context.Context) error {
	current, err := s.active(ctx)
	if err != nil || current == nil {
		return err
	}
	var owner database.ServerUpgrade
	if err = s.DB.WithContext(ctx).Where("id=? AND instance_key=?", current.ID, s.instanceKey).First(&owner).Error; err != nil {
		return err
	}
	if owner.OwnerPID != os.Getpid() && owner.OwnerPID > 0 {
		process, findErr := os.FindProcess(owner.OwnerPID)
		if findErr == nil {
			signalErr := process.Signal(syscall.Signal(0))
			process.Release()
			if signalErr == nil || errors.Is(signalErr, os.ErrPermission) {
				return nil
			}
		}
	}
	// The Server is replaced last. A matching running version also confirms an
	// interrupted checkpoint immediately after the final rename.
	if (current.Status == "restarting" || current.Status == "installing") && current.TargetVersion == s.config.Version {
		return s.finish(current.ID, "succeeded", nil)
	}
	failure := errors.New("升级尚未完成时 Server 已重新启动")
	plan := Restart{ID: current.ID, Directory: filepath.Join(s.config.Root, current.ID), Executable: s.config.Executable}
	if message, readErr := os.ReadFile(filepath.Join(plan.Directory, "restart-error")); readErr == nil {
		failure = fmt.Errorf("重启新版失败，已恢复原文件: %s", message)
	}
	if err = plan.Rollback(); err != nil {
		failure = fmt.Errorf("%v; 恢复原文件失败: %w", failure, err)
	}
	return s.finish(current.ID, "failed", failure)
}

func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.workers.Wait()
}
