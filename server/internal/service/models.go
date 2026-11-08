package service

import (
	"database/sql"

	"github.com/memory-co/agentuse/server/internal/store"
)

type Agent = store.Agent

const agentCols = store.AgentCols

var scanAgent = store.ScanAgent

type AttachmentAccess = store.AttachmentAccess

const attachmentCols = store.AttachmentCols

var scanAttachment = store.ScanAttachment

const DevicePending = store.DevicePending
const DeviceOnline = store.DeviceOnline
const DeviceOffline = store.DeviceOffline

type Device = store.Device
type DeviceHarness = store.DeviceHarness
type ReportedHarness = store.ReportedHarness

const deviceCols = store.DeviceCols

type Harness = store.Harness
type Question = store.Question

const RunCapability = store.RunCapability

type Run = store.Run
type Delivery = store.Delivery
type Reply = store.Reply
type ConversationDetail = store.ConversationDetail

const ConversationIdle = store.ConversationIdle
const ConversationPending = store.ConversationPending
const ConversationRunning = store.ConversationRunning
const ConversationWaiting = store.ConversationWaiting
const ConversationResolved = store.ConversationResolved
const WorkCoordinator = store.WorkCoordinator
const WorkThread = store.WorkThread
const RoleUserMsg = store.RoleUserMsg
const RoleAgentMsg = store.RoleAgentMsg

type Conversation = store.Conversation
type Message = store.Message
type ConversationEvent = store.ConversationEvent

const conversationCols = store.ConversationCols
const messageCols = store.MessageCols

var scanMessage = store.ScanMessage

const RoleAdmin = store.RoleAdmin
const RoleUser = store.RoleUser

type User = store.User
type Work = store.Work
type WorkPatch = store.WorkPatch

const workCols = store.WorkCols

var scanWork = store.ScanWork
var ErrNotFound = store.ErrNotFound
var HashSecret = store.HashSecret
var RandomToken = store.RandomToken
var randomID = store.RandomID
var newUUID = store.NewUUID

func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
func nullStr(v sql.NullString) string { return v.String }
