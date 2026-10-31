package service

import (
	"database/sql"
	"github.com/memory-co/agentuse/server/internal/store"
)

type Agent = store.Agent
type AgentPatch = store.AgentPatch

const agentCols = store.AgentCols

var scanAgent = store.ScanAgent

type AttachmentAccess = store.AttachmentAccess

const attachmentCols = store.AttachmentCols

var scanAttachment = store.ScanAttachment

const DaemonPending = store.DaemonPending
const DaemonOnline = store.DaemonOnline
const DaemonOffline = store.DaemonOffline

type Daemon = store.Daemon
type DaemonHarness = store.DaemonHarness
type ReportedHarness = store.ReportedHarness

const daemonCols = store.DaemonCols
const HarnessBuiltin = store.HarnessBuiltin
const HarnessCustom = store.HarnessCustom

type Harness = store.Harness
type Question = store.Question

const QuestionPending = store.QuestionPending
const QuestionAnswered = store.QuestionAnswered
const QuestionExpired = store.QuestionExpired
const MultiAgent = store.MultiAgent
const RunCapability = store.RunCapability

type Run = store.Run
type Delivery = store.Delivery
type MultiReply = store.MultiReply
type RunClaim = store.RunClaim

const runCols = store.RunCols

var scanRun = store.ScanRun

const deliveryCols = store.DeliveryCols

type SessionDetail = store.SessionDetail

const SessionIdle = store.SessionIdle
const SessionPending = store.SessionPending
const SessionRunning = store.SessionRunning
const SessionWaiting = store.SessionWaiting
const SessionResolved = store.SessionResolved
const WorkCoordinator = store.WorkCoordinator
const WorkThread = store.WorkThread
const RoleUserMsg = store.RoleUserMsg
const RoleAssistantMsg = store.RoleAssistantMsg
const EventProgress = store.EventProgress
const EventPlan = store.EventPlan
const EventSteered = store.EventSteered
const EventQuestion = store.EventQuestion
const EventAnswered = store.EventAnswered
const EventQuestionExpired = store.EventQuestionExpired
const EventDone = store.EventDone
const EventFailed = store.EventFailed

type Session = store.Session
type Message = store.Message
type SessionEvent = store.SessionEvent

const sessionCols = store.SessionCols
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
