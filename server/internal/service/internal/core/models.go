package core

import (
	"database/sql"

	"github.com/memory-co/agentuse/server/internal/store"
)

type Agent = store.Agent

const AgentCols = store.AgentCols

var ScanAgent = store.ScanAgent

type AttachmentAccess = store.AttachmentAccess

const AttachmentCols = store.AttachmentCols

var ScanAttachment = store.ScanAttachment

const DevicePending = store.DevicePending
const DeviceOnline = store.DeviceOnline
const DeviceOffline = store.DeviceOffline

type Device = store.Device
type DeviceHarness = store.DeviceHarness
type ReportedHarness = store.ReportedHarness

const DeviceCols = store.DeviceCols

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
const RoleUserMsg = store.RoleUserMsg
const RoleAgentMsg = store.RoleAgentMsg
const RoleChildConversationMsg = store.RoleChildConversationMsg

type ConversationRound = store.ConversationRound

type Conversation = store.Conversation
type Message = store.Message
type ConversationEvent = store.ConversationEvent

const ConversationCols = store.ConversationCols
const MessageCols = store.MessageCols

var ScanMessage = store.ScanMessage

const RoleAdmin = store.RoleAdmin
const RoleUser = store.RoleUser

type User = store.User

var ErrNotFound = store.ErrNotFound
var HashSecret = store.HashSecret
var RandomToken = store.RandomToken
var RandomID = store.RandomID
var NewUUID = store.NewUUID

func NullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
func NullStr(v sql.NullString) string { return v.String }

type AgentSource = store.AgentSource
