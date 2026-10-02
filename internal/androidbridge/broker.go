// Package androidbridge transports admitted Core commands to a paired Android
// provider. It has no task, permission, or activity authority of its own.
package androidbridge

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/agentdock/internal/tool/command/session"
)

const ProtocolVersion = 1
const MaxRequestBytes = 256 * 1024
const MaxChunkBytes = 16 * 1024
const MaxCommands = 4
const maxJobs = 32
const leaseFreshness = 45 * time.Second

var (
	ErrUnauthorized = errors.New("invalid Android executor lease")
	ErrOffline      = errors.New("Android executor is offline or the selected backend is not ready")
	ErrConflict     = errors.New("another Android executor owns the active lease")
	ErrCapacity     = errors.New("Android executor capacity exhausted; reconcile existing operations")
	ErrProtocol     = errors.New("invalid Android executor protocol message")
)

type Capability struct {
	Ready bool   `json:"ready"`
	State string `json:"state"`
	UID   *int   `json:"uid,omitempty"`
}
type RegisterRequest struct {
	Protocol    int                   `json:"protocol"`
	WorkerID    string                `json:"worker_id"`
	ResumeToken string                `json:"resume_token,omitempty"`
	Backends    map[string]Capability `json:"backends"`
}
type Registration struct {
	Protocol   int    `json:"protocol"`
	NodeID     string `json:"node_id"`
	LeaseToken string `json:"lease_token"`
}
type Spec struct {
	Backend   string            `json:"backend"`
	Command   string            `json:"command"`
	Workdir   string            `json:"workdir"`
	Env       map[string]string `json:"env,omitempty"`
	TimeoutMS int64             `json:"timeout_ms"`
	TTY       bool              `json:"tty"`
}
type Input struct {
	Sequence uint64 `json:"sequence"`
	Data     []byte `json:"data"`
}
type Instruction struct {
	OperationID  string  `json:"operation_id"`
	Revision     uint64  `json:"revision"`
	Action       string  `json:"action"`
	Spec         *Spec   `json:"spec,omitempty"`
	Backend      string  `json:"backend"`
	StdoutOffset int64   `json:"stdout_offset"`
	StderrOffset int64   `json:"stderr_offset"`
	Cancel       bool    `json:"cancel"`
	EOF          bool    `json:"eof"`
	InputApplied uint64  `json:"input_applied"`
	Input        []Input `json:"input,omitempty"`
}
type Event struct {
	OutputLimited bool   `json:"output_limited,omitempty"`
	OperationID   string `json:"operation_id"`
	Revision      uint64 `json:"revision"`
	State         string `json:"state"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	Stdout        []byte `json:"stdout,omitempty"`
	Stderr        []byte `json:"stderr,omitempty"`
	StdoutOffset  int64  `json:"stdout_offset"`
	StderrOffset  int64  `json:"stderr_offset"`
	InputApplied  uint64 `json:"input_applied"`
}
type ExchangeRequest struct {
	Protocol int                   `json:"protocol"`
	Events   []Event               `json:"events"`
	Backends map[string]Capability `json:"backends"`
}
type ExchangeResponse struct {
	Protocol int           `json:"protocol"`
	NodeID   string        `json:"node_id"`
	Commands []Instruction `json:"commands"`
}

type Broker struct {
	mu       sync.Mutex
	nodeID   string
	workerID string
	token    string
	lastSeen time.Time
	backends map[string]Capability
	jobs     map[string]*job
	closed   bool
	clock    func() time.Time
}

func New(nodeID string) *Broker {
	return &Broker{nodeID: nodeID, jobs: map[string]*job{}, backends: map[string]Capability{}, clock: time.Now}
}

func validCapabilities(backends map[string]Capability) bool {
	if len(backends) > 2 {
		return false
	}
	for name, capability := range backends {
		if name != "termux_host" && name != "android_shizuku" {
			return false
		}
		if len(capability.State) > 80 {
			return false
		}
		if capability.UID != nil && (*capability.UID < 0 || *capability.UID > 2147483647) {
			return false
		}
	}
	return true
}

func (b *Broker) Register(r RegisterRequest) (Registration, error) {
	if r.Protocol != ProtocolVersion || len(r.WorkerID) < 16 || len(r.WorkerID) > 96 || !validCapabilities(r.Backends) {
		return Registration{}, ErrProtocol
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return Registration{}, ErrOffline
	}
	if b.token != "" {
		if r.WorkerID != b.workerID || (r.ResumeToken != "" && !sameToken(b.token, r.ResumeToken)) {
			return Registration{}, ErrConflict
		}
	} else {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return Registration{}, err
		}
		b.token, b.workerID = hex.EncodeToString(raw), r.WorkerID
	}
	b.backends = cloneCapabilities(r.Backends)
	b.lastSeen = b.clock()
	return Registration{Protocol: ProtocolVersion, NodeID: b.nodeID, LeaseToken: b.token}, nil
}

func sameToken(expected, actual string) bool {
	return len(expected) == 64 && len(actual) == 64 && subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}
func cloneCapabilities(value map[string]Capability) map[string]Capability {
	result := map[string]Capability{}
	for k, v := range value {
		if v.UID != nil {
			uid := *v.UID
			v.UID = &uid
		}
		result[k] = v
	}
	return result
}

func (b *Broker) Status() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return map[string]any{"protocol": ProtocolVersion, "node_id": b.nodeID, "connected": b.freshLocked(), "backends": cloneCapabilities(b.backends), "inflight": len(b.jobs)}
}
func (b *Broker) freshLocked() bool {
	return !b.closed && b.token != "" && !b.lastSeen.IsZero() && b.clock().Sub(b.lastSeen) <= leaseFreshness
}
func (b *Broker) Ready(backend string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.freshLocked() && b.backends[backend].Ready
}

func ValidateSpec(s Spec) error {
	if s.Backend != "termux_host" && s.Backend != "android_shizuku" {
		return ErrProtocol
	}
	if len(s.Command) == 0 || len(s.Command) > 16384 || strings.IndexByte(s.Command, 0) >= 0 {
		return ErrProtocol
	}
	if !path.IsAbs(s.Workdir) || path.Clean(s.Workdir) != s.Workdir || len(s.Workdir) > 4096 || strings.IndexByte(s.Workdir, 0) >= 0 {
		return ErrProtocol
	}
	if s.TimeoutMS < 1 || s.TimeoutMS > 86400000 || len(s.Env) > 64 {
		return ErrProtocol
	}
	size := 0
	for k, v := range s.Env {
		size += len(k) + len(v)
		if len(k) == 0 || len(k) > 128 || strings.ContainsAny(k, "=\x00") || strings.IndexByte(v, 0) >= 0 {
			return ErrProtocol
		}
	}
	if size > 16384 {
		return ErrProtocol
	}
	return nil
}

func (b *Broker) Start(ctx context.Context, id string, spec Spec, stdout, stderr io.Writer, state func(string)) (session.ExternalProcess, error) {
	if err := ValidateSpec(spec); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.freshLocked() || !b.backends[spec.Backend].Ready {
		return nil, ErrOffline
	}
	if len(b.jobs) >= maxJobs {
		return nil, ErrCapacity
	}
	if _, ok := b.jobs[id]; ok {
		return nil, ErrConflict
	}
	copied := spec
	copied.Env = map[string]string{}
	for k, v := range spec.Env {
		copied.Env[k] = v
	}
	j := &job{broker: b, id: id, spec: copied, stdout: stdout, stderr: stderr, state: state, revision: 1, done: make(chan struct{}),
		changed: make(chan struct{}, 1), deadline: b.clock().Add(time.Duration(spec.TimeoutMS) * time.Millisecond), exitCode: -1}
	b.jobs[id] = j
	return j, nil
}

func (b *Broker) Exchange(token string, r ExchangeRequest) (ExchangeResponse, error) {
	if r.Protocol != ProtocolVersion || len(r.Events) > MaxCommands || !validCapabilities(r.Backends) {
		return ExchangeResponse{}, ErrProtocol
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || !sameToken(b.token, token) {
		return ExchangeResponse{}, ErrUnauthorized
	}
	// Validate the entire message before applying any part of the batch.
	seen := map[string]bool{}
	for _, e := range r.Events {
		if seen[e.OperationID] {
			return ExchangeResponse{}, ErrProtocol
		}
		seen[e.OperationID] = true
		if err := b.validateEventLocked(e); err != nil {
			return ExchangeResponse{}, err
		}
	}
	b.lastSeen = b.clock()
	b.backends = cloneCapabilities(r.Backends)
	for _, e := range r.Events {
		b.applyEventLocked(e)
	}
	ids := make([]string, 0, len(b.jobs))
	for id := range b.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	response := ExchangeResponse{Protocol: ProtocolVersion, NodeID: b.nodeID, Commands: []Instruction{}}
	// Round-robin by last offered time prevents a long running command from
	// starving the other admitted sessions when the batch is full.
	sort.SliceStable(ids, func(i, j int) bool { return b.jobs[ids[i]].lastOffered.Before(b.jobs[ids[j]].lastOffered) })
	for _, id := range ids {
		j := b.jobs[id]
		if !j.deadline.After(b.clock()) {
			j.cancel = true
		}
		if j.cancel && !j.offered {
			j.finishLocked(-1, session.ErrRemoteTimeout)
			continue
		}
		instruction := Instruction{OperationID: j.id, Revision: j.revision, Action: "observe", Backend: j.spec.Backend,
			StdoutOffset: j.outOffset, StderrOffset: j.errOffset, Cancel: j.cancel, EOF: j.eof, InputApplied: j.inputApplied}
		if !j.offered {
			instruction.Action = "start"
			copy := j.spec
			instruction.Spec = &copy
			j.offered = true
		}
		for _, in := range j.input {
			instruction.Input = append(instruction.Input, Input{Sequence: in.Sequence, Data: append([]byte(nil), in.Data...)})
		}
		j.lastOffered = b.clock()
		response.Commands = append(response.Commands, instruction)
		if len(response.Commands) == MaxCommands {
			break
		}
	}
	return response, nil
}

func (b *Broker) validateEventLocked(e Event) error {
	j := b.jobs[e.OperationID]
	// A response retried after its terminal acknowledgement is harmless.
	if j == nil {
		return nil
	}
	if e.Revision == 0 || e.Revision > j.revision || len(e.Stdout) > MaxChunkBytes || len(e.Stderr) > MaxChunkBytes {
		return ErrProtocol
	}
	if e.Revision < j.revision {
		return nil
	}
	if e.StdoutOffset != j.outOffset || e.StderrOffset != j.errOffset || e.InputApplied < j.inputApplied || e.InputApplied > j.inputIssued {
		return ErrProtocol
	}
	switch e.State {
	case "queued", "running", "cancel_requested", "unknown":
		if e.ExitCode != nil {
			return ErrProtocol
		}
	case "exited", "cancelled", "timeout", "not_started":
		if e.ExitCode == nil || *e.ExitCode < -1 || *e.ExitCode > 255 {
			return ErrProtocol
		}
	default:
		return ErrProtocol
	}
	return nil
}
func (b *Broker) applyEventLocked(e Event) {
	j := b.jobs[e.OperationID]
	if j == nil || e.Revision < j.revision {
		return
	}
	_, _ = j.stdout.Write(e.Stdout)
	_, _ = j.stderr.Write(e.Stderr)
	j.outOffset += int64(len(e.Stdout))
	j.errOffset += int64(len(e.Stderr))
	j.revision++
	j.inputApplied = e.InputApplied
	kept := j.input[:0]
	for _, in := range j.input {
		if in.Sequence > j.inputApplied {
			kept = append(kept, in)
		}
	}
	j.input = kept
	select {
	case j.changed <- struct{}{}:
	default:
	}
	switch e.State {
	case "exited":
		var err error
		if *e.ExitCode != 0 {
			err = fmt.Errorf("remote command exited with code %d", *e.ExitCode)
		}
		if e.OutputLimited {
			err = errors.New("remote output limit reached; retained output is incomplete")
		}
		j.finishLocked(*e.ExitCode, err)
	case "cancelled":
		j.finishLocked(*e.ExitCode, session.ErrRemoteCancelled)
	case "timeout":
		j.finishLocked(*e.ExitCode, session.ErrRemoteTimeout)
	case "not_started":
		j.finishLocked(*e.ExitCode, errors.New("Android provider rejected the command before start"))
	default:
		j.state(e.State)
	}
}

// Disconnect never interprets the missing provider as successful cancellation.
// Admitted jobs remain queryable by the same lease after reconnect.
func (b *Broker) Disconnect(token string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !sameToken(b.token, token) {
		return ErrUnauthorized
	}
	b.lastSeen = time.Time{}
	b.backends = map[string]Capability{}
	for _, j := range b.jobs {
		j.cancel = true
		j.state("unknown")
	}
	return nil
}
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.backends = map[string]Capability{}
	for _, j := range b.jobs {
		j.finishLocked(-1, session.ErrOutcomeUnknown)
	}
	b.token = ""
}

type job struct {
	broker                    *Broker
	id                        string
	spec                      Spec
	stdout, stderr            io.Writer
	state                     func(string)
	done                      chan struct{}
	changed                   chan struct{}
	revision                  uint64
	offered                   bool
	lastOffered               time.Time
	deadline                  time.Time
	outOffset, errOffset      int64
	cancel, eof, complete     bool
	input                     []Input
	inputIssued, inputApplied uint64
	exitCode                  int
	err                       error
}

func (j *job) finishLocked(code int, err error) {
	if j.complete {
		return
	}
	j.complete = true
	j.exitCode, j.err = code, err
	delete(j.broker.jobs, j.id)
	close(j.done)
}
func (j *job) Stdin() io.WriteCloser { return jobInput{j} }
func (j *job) Wait() (int, error) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-j.done:
			j.broker.mu.Lock()
			code, err := j.exitCode, j.err
			j.broker.mu.Unlock()
			return code, err
		case <-tick.C:
			b := j.broker
			b.mu.Lock()
			if !j.complete {
				if !j.deadline.After(b.clock()) {
					j.cancel = true
				}
				if !b.freshLocked() {
					j.state("unknown")
				} else if j.cancel {
					j.state("cancel_requested")
				}
			}
			b.mu.Unlock()
		}
	}
}
func (j *job) Kill() error {
	b := j.broker
	b.mu.Lock()
	defer b.mu.Unlock()
	if j.complete {
		return nil
	}
	j.cancel = true
	if !j.offered {
		j.finishLocked(-1, session.ErrRemoteCancelled)
	}
	return nil
}

type jobInput struct{ j *job }

func (w jobInput) Write(data []byte) (int, error) {
	j := w.j
	b := j.broker
	if len(data) == 0 {
		return 0, nil
	}
	if len(data) > 16384 {
		return 0, ErrCapacity
	}
	b.mu.Lock()
	if j.complete || j.eof || j.cancel {
		b.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	if len(j.input) >= 4 {
		b.mu.Unlock()
		return 0, ErrCapacity
	}
	j.inputIssued++
	seq := j.inputIssued
	j.input = append(j.input, Input{Sequence: seq, Data: append([]byte(nil), data...)})
	b.mu.Unlock()
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for {
		b.mu.Lock()
		applied := j.inputApplied >= seq
		b.mu.Unlock()
		if applied {
			return len(data), nil
		}
		select {
		case <-j.changed:
		case <-j.done:
			return 0, io.ErrClosedPipe
		case <-timer.C:
			return 0, errors.New("remote stdin acknowledgement missing; input outcome unknown, do not resend")
		}
	}
}
func (w jobInput) Close() error {
	b := w.j.broker
	b.mu.Lock()
	defer b.mu.Unlock()
	w.j.eof = true
	return nil
}
