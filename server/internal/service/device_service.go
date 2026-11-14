package service

// DeviceService owns registration, presence and reported execution capabilities.
// Delivery retirement and attachment attribution use their owners in the same tx.
type DeviceService struct {
	*serviceRuntime
	runs          *RunService
	works         *WorkService
	conversations *ConversationService
}
