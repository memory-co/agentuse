package device

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/memory-co/agentuse/server/internal/service/internal/core"
	"github.com/memory-co/agentuse/server/internal/store"
)

// runtimeErr 检查运行时 (deviceID, harness) 现在能不能派活:这台 device 上报过这个 harness、最近一次上报里有、用户开启了。
// 能派返回 nil,否则返回给用户看的原因。device 离线不算：上线后会接着派。
func (s *Service) CheckRuntime(ctx context.Context, q core.Querier, deviceID, harness string) error {
	var available, enabled bool
	err := q.QueryRowContext(ctx,
		store.RuntimeErrSelect,
		deviceID, harness).Scan(&available, &enabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w:device %s 上没有 harness %s", core.ErrRuntimeUnavailable, deviceID, harness)
	case err != nil:
		return err
	case !available:
		return fmt.Errorf("%w:device %s 最近一次连上时没检测到 harness %s,请在那台电脑上装好后重启 device", core.ErrRuntimeUnavailable, deviceID, harness)
	case !enabled:
		return fmt.Errorf("%w:device %s 上没有开启 harness %s,请先在 device 页面打开它", core.ErrRuntimeUnavailable, deviceID, harness)
	}
	return nil
}
