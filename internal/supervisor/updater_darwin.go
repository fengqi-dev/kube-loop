//go:build darwin

package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	supervisorprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type journal struct {
	RequestID string `json:"requestId"`
	Phase     string `json:"phase"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
}

type Updater struct {
	config         Config
	worker         WorkerController
	mu             sync.Mutex
	readyTimeout   time.Duration
	readyInterval  time.Duration
	artifactUID    int
	verifyArtifact func(context.Context, string, supervisorprotocol.UpdateManifest) error
}

func NewUpdater(config Config, worker WorkerController, artifactUID int) *Updater {
	updater := &Updater{
		config: config, worker: worker,
		readyTimeout: 20 * time.Second, readyInterval: 100 * time.Millisecond, artifactUID: artifactUID,
	}
	updater.verifyArtifact = updater.verifyWorkerIdentity
	return updater
}

func (u *Updater) Status(ctx context.Context) (supervisorprotocol.WorkerStatus, error) {
	status, err := u.worker.Status(ctx)
	if status.SHA256 == "" {
		status.SHA256, _ = fileSHA256(u.config.WorkerBinaryPath)
	}
	return status, err
}

func (u *Updater) Recover(ctx context.Context) error {
	if !fileExists(u.config.JournalPath()) {
		return nil
	}
	status, err := u.worker.Status(ctx)
	if err == nil && status.Running && status.CoreReady {
		return os.Remove(u.config.JournalPath())
	}
	if !fileExists(u.config.PreviousPath()) {
		return fmt.Errorf("update journal exists but no previous worker is available")
	}
	rolledBack, rollbackErr := u.rollback(ctx, fmt.Errorf("recover interrupted update"))
	if !rolledBack {
		return rollbackErr
	}
	return os.Remove(u.config.JournalPath())
}

func (u *Updater) Update(
	ctx context.Context,
	manifest supervisorprotocol.UpdateManifest,
	body io.Reader,
) (response supervisorprotocol.Response) {
	response.Protocol = supervisorprotocol.Version
	response.Channel = u.config.Channel
	if err := validateManifest(u.config, manifest); err != nil {
		response.Error = err.Error()
		return response
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	lock, err := u.acquireLock()
	if err != nil {
		response.Error = err.Error()
		return response
	}
	defer func() {
		_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
		_ = lock.Close()
	}()

	before, err := u.worker.Status(ctx)
	if err != nil && !before.Installed {
		response.Error = fmt.Sprintf("query worker before update: %v", err)
		return response
	}
	if len(before.ActiveSessions) != 0 && !manifest.Force {
		response.Error = "worker has active sessions; disconnect TUN or use force"
		response.Worker = before
		return response
	}

	staged := filepath.Join(filepath.Dir(u.config.WorkerBinaryPath), "."+u.config.WorkerLabel+".staged")
	if err := u.stageWorker(ctx, staged, manifest, body); err != nil {
		response.Error = err.Error()
		return response
	}
	defer func() { _ = os.Remove(staged) }()
	if err := u.writeJournal(journal{
		RequestID: manifest.RequestID,
		Phase:     "staged",
		Version:   manifest.Version,
		SHA256:    manifest.SHA256,
	}); err != nil {
		response.Error = err.Error()
		return response
	}

	rolledBack, updateErr := u.activate(ctx, staged, manifest)
	response.RolledBack = rolledBack
	if rolledBack {
		_ = os.Remove(u.config.JournalPath())
	}
	response.PreviousAvailable = fileExists(u.config.PreviousPath())
	response.Worker, _ = u.Status(ctx)
	if updateErr != nil {
		response.Error = updateErr.Error()
		return response
	}
	response.OK = true
	_ = os.Remove(u.config.JournalPath())
	return response
}

func validateManifest(config Config, manifest supervisorprotocol.UpdateManifest) error {
	if manifest.SchemaVersion != supervisorprotocol.SchemaVersion {
		return fmt.Errorf("unsupported manifest schema %d", manifest.SchemaVersion)
	}
	if !requestIDPattern.MatchString(manifest.RequestID) {
		return fmt.Errorf("invalid request ID")
	}
	if manifest.Channel != config.Channel {
		return fmt.Errorf("manifest channel %q does not match %q", manifest.Channel, config.Channel)
	}
	if manifest.Version == "" || manifest.WorkerProtocol <= 0 {
		return fmt.Errorf("worker version and protocol are required")
	}
	if manifest.MinimumSupervisorProtocol > supervisorprotocol.Version {
		return fmt.Errorf("supervisor protocol %d is too old", supervisorprotocol.Version)
	}
	if manifest.Size <= 0 || manifest.Size > supervisorprotocol.MaxWorkerBytes {
		return fmt.Errorf("worker size %d is outside 1..%d", manifest.Size, supervisorprotocol.MaxWorkerBytes)
	}
	decoded, err := hex.DecodeString(manifest.SHA256)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("invalid worker SHA-256")
	}
	return nil
}

func (u *Updater) acquireLock() (*os.File, error) {
	if err := os.MkdirAll(u.config.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create supervisor state: %w", err)
	}
	lock, err := os.OpenFile(u.config.LockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open update lock: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("another worker update is in progress")
	}
	return lock, nil
}

func (u *Updater) stageWorker(
	ctx context.Context,
	path string,
	manifest supervisorprotocol.UpdateManifest,
	body io.Reader,
) error {
	//nolint:gosec // The staged worker must be executable for identity verification.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if os.IsExist(err) {
		_ = os.Remove(path)
		//nolint:gosec // The staged worker must be executable for identity verification.
		file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	}
	if err != nil {
		return fmt.Errorf("create staged worker: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.CopyN(io.MultiWriter(file, hash), body, manifest.Size)
	if copyErr == nil && written != manifest.Size {
		copyErr = io.ErrUnexpectedEOF
	}
	if copyErr == nil {
		var extra [1]byte
		if count, extraErr := body.Read(extra[:]); count != 0 || (extraErr != nil && !errors.Is(extraErr, io.EOF)) {
			copyErr = fmt.Errorf("worker payload contains extra bytes")
		}
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("receive staged worker: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close staged worker: %w", closeErr)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != manifest.SHA256 {
		_ = os.Remove(path)
		return fmt.Errorf("worker SHA-256 mismatch")
	}
	if err := verifyMachO(path); err != nil {
		_ = os.Remove(path)
		return err
	}
	if err := u.verifyArtifact(ctx, path, manifest); err != nil {
		_ = os.Remove(path)
		return err
	}
	if u.config.Channel == releaseChannel {
		if output, err := exec.Command("/usr/bin/codesign", "--verify", "--strict", path).CombinedOutput(); err != nil {
			_ = os.Remove(path)
			return fmt.Errorf("verify worker signature: %w: %s", err, output)
		}
	}
	return nil
}

type workerIdentity struct {
	Kind     string `json:"kind"`
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if w.buffer.Len()+len(p) > w.limit {
		return 0, fmt.Errorf("identity output exceeds %d bytes", w.limit)
	}
	return w.buffer.Write(p)
}

func (u *Updater) verifyWorkerIdentity(
	ctx context.Context,
	path string,
	manifest supervisorprotocol.UpdateManifest,
) error {
	account, err := user.LookupId(strconv.Itoa(u.artifactUID))
	if err != nil {
		return fmt.Errorf("resolve artifact owner: %w", err)
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		return fmt.Errorf("parse artifact owner group: %w", err)
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(verifyCtx, path, "identity")
	if u.artifactUID < 0 || uint64(u.artifactUID) > math.MaxUint32 {
		return fmt.Errorf("artifact owner UID %d is outside uint32 range", u.artifactUID)
	}
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
		Uid: uint32(u.artifactUID), Gid: uint32(gid),
	}}
	output := &limitedBuffer{limit: 4 << 10}
	command.Stdout = output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("verify worker identity: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output.buffer.Bytes()))
	decoder.DisallowUnknownFields()
	var identity workerIdentity
	if err := decoder.Decode(&identity); err != nil {
		return fmt.Errorf("decode worker identity: %w", err)
	}
	wrongIdentity := identity.Kind != "kubeloop-helper" || identity.Version != manifest.Version ||
		identity.Protocol != manifest.WorkerProtocol
	if wrongIdentity {
		return fmt.Errorf(
			"worker identity mismatch: kind=%q version=%q protocol=%d",
			identity.Kind,
			identity.Version,
			identity.Protocol,
		)
	}
	return nil
}

func verifyMachO(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	var magic [4]byte
	if _, err := io.ReadFull(file, magic[:]); err != nil {
		return fmt.Errorf("read worker executable header: %w", err)
	}
	valid := magic == [4]byte{0xcf, 0xfa, 0xed, 0xfe} ||
		magic == [4]byte{0xfe, 0xed, 0xfa, 0xcf} ||
		magic == [4]byte{0xca, 0xfe, 0xba, 0xbe} ||
		magic == [4]byte{0xbe, 0xba, 0xfe, 0xca}
	if !valid {
		return fmt.Errorf("worker is not a Mach-O executable")
	}
	return nil
}

func (u *Updater) activate(
	ctx context.Context,
	staged string,
	manifest supervisorprotocol.UpdateManifest,
) (bool, error) {
	if err := u.worker.Stop(ctx); err != nil {
		return false, err
	}
	previous := u.config.PreviousPath()
	_ = os.Remove(previous)
	if fileExists(u.config.WorkerBinaryPath) {
		if err := os.Rename(u.config.WorkerBinaryPath, previous); err != nil {
			_ = u.worker.Start(ctx)
			return false, fmt.Errorf("preserve previous worker: %w", err)
		}
	}
	if err := u.writeJournal(journal{
		RequestID: manifest.RequestID,
		Phase:     "swapping",
		Version:   manifest.Version,
		SHA256:    manifest.SHA256,
	}); err != nil {
		return u.rollback(ctx, err)
	}
	if err := os.Rename(staged, u.config.WorkerBinaryPath); err != nil {
		return u.rollback(ctx, fmt.Errorf("activate staged worker: %w", err))
	}
	//nolint:gosec // The installed worker must be executable by launchd.
	if err := os.Chmod(u.config.WorkerBinaryPath, 0o755); err != nil {
		return u.rollback(ctx, fmt.Errorf("secure worker executable: %w", err))
	}
	if err := syncDir(filepath.Dir(u.config.WorkerBinaryPath)); err != nil {
		return u.rollback(ctx, fmt.Errorf("sync worker directory: %w", err))
	}
	if err := u.worker.Start(ctx); err != nil {
		return u.rollback(ctx, err)
	}
	if err := u.waitReady(ctx, manifest); err != nil {
		return u.rollback(ctx, err)
	}
	return false, nil
}

func (u *Updater) waitReady(ctx context.Context, manifest supervisorprotocol.UpdateManifest) error {
	waitCtx, cancel := context.WithTimeout(ctx, u.readyTimeout)
	defer cancel()
	var lastErr error
	for {
		status, err := u.worker.Status(waitCtx)
		if err == nil && status.Running && status.CoreReady && status.Version == manifest.Version &&
			status.Protocol == manifest.WorkerProtocol && status.SHA256 == manifest.SHA256 {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf(
				"worker readiness mismatch: version=%q protocol=%d coreReady=%v sha256=%q",
				status.Version,
				status.Protocol,
				status.CoreReady,
				status.SHA256,
			)
		}
		timer := time.NewTimer(u.readyInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return fmt.Errorf("worker did not become ready: %w", lastErr)
		case <-timer.C:
		}
	}
}

func (u *Updater) rollback(ctx context.Context, cause error) (bool, error) {
	_ = u.worker.Stop(ctx)
	if !fileExists(u.config.PreviousPath()) {
		return false, cause
	}
	_ = os.Remove(u.config.WorkerBinaryPath)
	if err := os.Rename(u.config.PreviousPath(), u.config.WorkerBinaryPath); err != nil {
		return false, errors.Join(cause, fmt.Errorf("restore previous worker: %w", err))
	}
	if err := u.worker.Start(ctx); err != nil {
		return false, errors.Join(cause, fmt.Errorf("restart previous worker: %w", err))
	}
	if err := u.waitAnyReady(ctx); err != nil {
		return false, errors.Join(cause, fmt.Errorf("previous worker did not recover: %w", err))
	}
	return true, cause
}

func (u *Updater) waitAnyReady(ctx context.Context) error {
	waitCtx, cancel := context.WithTimeout(ctx, u.readyTimeout)
	defer cancel()
	var lastErr error
	for {
		status, err := u.worker.Status(waitCtx)
		if err == nil && status.Running && status.CoreReady {
			return nil
		}
		lastErr = err
		timer := time.NewTimer(u.readyInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if lastErr != nil {
				return lastErr
			}
			return waitCtx.Err()
		case <-timer.C:
		}
	}
}

func (u *Updater) Rollback(ctx context.Context) supervisorprotocol.Response {
	u.mu.Lock()
	defer u.mu.Unlock()
	response := supervisorprotocol.Response{Protocol: supervisorprotocol.Version, Channel: u.config.Channel}
	if !fileExists(u.config.PreviousPath()) {
		response.Error = "no previous worker is available"
		return response
	}
	rolledBack, err := u.rollback(ctx, fmt.Errorf("manual rollback"))
	response.RolledBack = rolledBack
	response.Worker, _ = u.Status(ctx)
	if err != nil && !rolledBack {
		response.Error = err.Error()
		return response
	}
	response.OK = true
	return response
}

func (u *Updater) Restart(ctx context.Context) supervisorprotocol.Response {
	u.mu.Lock()
	defer u.mu.Unlock()
	response := supervisorprotocol.Response{Protocol: supervisorprotocol.Version, Channel: u.config.Channel}
	if err := u.worker.Stop(ctx); err != nil {
		response.Error = err.Error()
		return response
	}
	if err := u.worker.Start(ctx); err != nil {
		response.Error = err.Error()
		return response
	}
	response.Worker, _ = u.Status(ctx)
	response.OK = response.Worker.Running && response.Worker.CoreReady
	if !response.OK {
		response.Error = "worker did not become ready after restart"
	}
	return response
}

func (u *Updater) writeJournal(value journal) error {
	if err := os.MkdirAll(u.config.StateDir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	tmp := u.config.JournalPath() + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if file, err := os.OpenFile(tmp, os.O_RDWR, 0); err == nil {
		_ = file.Sync()
		_ = file.Close()
	}
	if err := os.Rename(tmp, u.config.JournalPath()); err != nil {
		return err
	}
	return syncDir(u.config.StateDir)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
