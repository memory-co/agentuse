package service

// WorkService owns work membership, coordinator grants and summary deduplication.
// It uses conversation operations to create threads and deliver summaries.
type WorkService struct {
	*serviceRuntime
	users         *UserService
	devices       *DeviceService
	conversations *ConversationService
}
