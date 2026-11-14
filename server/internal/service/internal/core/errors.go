package core

import (
	"errors"
)

var ErrAttachmentRun = errors.New("上传附件需要当前设备在本会话的活动 run")
var ErrAttachmentCursor = errors.New("游标不合法")
var ErrAttachmentUnauthorized = errors.New("附件凭据无效或已过期")
var ErrAttachmentQuota = errors.New("附件超过会话容量限制")
var ErrAttachmentCapability = errors.New("device 尚不支持附件，请先升级并连接一次")
var ErrAttachmentPathConflict = errors.New("文件路径与已有文件冲突")
var ErrAttachmentReferenced = errors.New("附件已被消息引用，请保留到对话删除时一起清理")
var ErrConversationBusy = errors.New("会话还在等 harness 回复")
var ErrRuntimeUnavailable = errors.New("harness 不可用")
var ErrConversationState = errors.New("会话状态不对")
var ErrCoordinator = errors.New("这是工作的协调者，不能单独操作，请操作整个工作")
var ErrQuestionClosed = errors.New("request is no longer awaiting a reply")
