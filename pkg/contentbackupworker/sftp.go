package contentbackupworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// SFTPRemoteStore is the second RemoteStore implementation (design doc 9.2). It exists
// because the real target only speaks TLS 1.0 on its FTPS port (design doc 8.2, 2026-09-18
// probe) while its SSH port runs a current OpenSSH: SFTP is the transport that can actually
// be secured. The completion protocol is the same as FTPS (design doc 6.3): write to a
// job+lease-token temp name, read the remote bytes back and compare the digest, then rename
// without ever overwriting an existing final file. Credentials still come only from the
// daemon's deployment variables; the pin is the SHA-256 of the SSH host public key.
var _ RemoteStore = (*SFTPRemoteStore)(nil)

// sftpSentinel is an SFTP flavoured error that *is* the corresponding FTPS sentinel for
// errors.Is: T05's classifier and T10's reader keep matching on the ErrFTPS* values, so both
// transports select the same design doc 6.2 row without a second classification table,
// while the message a human reads names the protocol that actually failed.
type sftpSentinel struct {
	msg string
	is  error
}

func (s *sftpSentinel) Error() string { return s.msg }

func (s *sftpSentinel) Is(target error) bool { return target == s.is }

var (
	ErrSFTPConfig       = &sftpSentinel{"content backup sftp: target configuration is unusable", ErrFTPSConfig}
	ErrSFTPInvalidJob   = &sftpSentinel{"content backup sftp: job or lease identity does not match the configured target", ErrFTPSInvalidJob}
	ErrSFTPInvalidPin   = &sftpSentinel{"content backup sftp: sftp_host_key_sha256 must be 64 lowercase hex characters", ErrFTPSInvalidPin}
	ErrSFTPHostKey      = &sftpSentinel{"content backup sftp: ssh host key does not match the pinned sha256", ErrFTPSCertificate}
	ErrSFTPAuth         = &sftpSentinel{"content backup sftp: server rejected the credentials", ErrFTPSAuth}
	ErrSFTPPermission   = &sftpSentinel{"content backup sftp: server denied the operation", ErrFTPSPermission}
	ErrSFTPConflict     = &sftpSentinel{"content backup sftp: remote_conflict: final remote file exists with different content", ErrFTPSConflict}
	ErrSFTPHashMismatch = &sftpSentinel{"content backup sftp: read-back digest does not match compressed_sha256", ErrFTPSHashMismatch}
	ErrSFTPLocalSource  = &sftpSentinel{"content backup sftp: local source stream failed or has the wrong length", ErrFTPSLocalSource}
	ErrSFTPMissing      = &sftpSentinel{"content backup sftp: remote file is missing", ErrFTPSMissing}
	ErrSFTPTimeout      = &sftpSentinel{"content backup sftp: operation timed out", ErrFTPSTimeout}
	ErrSFTPNetwork      = &sftpSentinel{"content backup sftp: retryable network failure", ErrFTPSNetwork}
	ErrSFTPUnsupported  = &sftpSentinel{"content backup sftp: remote lacks the required rename/duplicate-name semantics", ErrFTPSUnsupported}
)

const (
	sftpTempSuffix    = ftpsTempSuffix
	sftpShutdownGrace = ftpsShutdownGrace

	sftpDefaultConnectTimeout   = 10 * time.Second
	sftpDefaultOperationTimeout = 120 * time.Second
	sftpKeepaliveInterval       = 15 * time.Second
	sftpMaxIdle                 = 90 * time.Second
)

// sftpHostKeyAlgorithms is the client's host key preference, Ed25519 first. It is what
// `ssh-keyscan -t ed25519` in the operator hint relies on: a server that has an Ed25519
// key will always present that one, so the pinned fingerprint and the negotiated key agree.
var sftpHostKeyAlgorithms = []string{
	ssh.KeyAlgoED25519,
	ssh.KeyAlgoECDSA256,
	ssh.KeyAlgoECDSA384,
	ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512,
	ssh.KeyAlgoRSASHA256,
}

// SFTPTarget is the immutable target identity for the SFTP transport. HostKeySHA256 is the
// lowercase hex SHA-256 over the host public key wire format, i.e. the same bytes OpenSSH's
// `ssh-keygen -lf` hashes (that tool prints them base64 encoded after "SHA256:").
type SFTPTarget struct {
	TargetID      string
	SiteID        string
	Host          string
	Port          int
	Username      string
	Password      string
	HostKeySHA256 string
	// BaseDir is the physical directory the logical "/{site}/..." tree lives under. SFTP
	// accounts are normally not chrooted, so "/" is the real root and unwritable; "" means
	// the login directory the server reports for the account (resolved once per session).
	BaseDir          string
	ConnectTimeout   time.Duration
	OperationTimeout time.Duration
	Limiter          *BandwidthLimiter
}

type SFTPRemoteStore struct {
	target    SFTPTarget
	pin       [sha256.Size]byte
	dirMu     sync.Mutex
	dirCached map[string]struct{}

	sessMu sync.Mutex
	idle   []*sftpSession
}

func NewSFTPRemoteStore(target SFTPTarget) (*SFTPRemoteStore, error) {
	// Construction-time failures all hang off ErrSFTPConfig (→ ErrFTPSConfig): they describe
	// a target that cannot work, and backing off 16 times will not fix a config (design doc 6.2).
	if target.TargetID == "" {
		return nil, fmt.Errorf("%w: target_id is required", ErrSFTPConfig)
	}
	if err := contentbackup.ValidateSiteLabel(target.SiteID); err != nil {
		return nil, fmt.Errorf("%w: invalid site label: %v", ErrSFTPConfig, err)
	}
	if target.Host == "" {
		return nil, fmt.Errorf("%w: host is required", ErrSFTPConfig)
	}
	if target.Port < 1 || target.Port > 65535 {
		return nil, fmt.Errorf("%w: port out of range: %d", ErrSFTPConfig, target.Port)
	}
	if target.Username == "" {
		return nil, fmt.Errorf("%w: username is required", ErrSFTPConfig)
	}
	if len(target.HostKeySHA256) != 64 {
		return nil, fmt.Errorf("%w: got %d characters", ErrSFTPInvalidPin, len(target.HostKeySHA256))
	}
	raw, err := hex.DecodeString(target.HostKeySHA256)
	if err != nil || strings.ToLower(target.HostKeySHA256) != target.HostKeySHA256 {
		return nil, fmt.Errorf("%w: %v", ErrSFTPInvalidPin, err)
	}
	if err := contentbackup.ValidateRemoteBaseDir(target.BaseDir); err != nil {
		return nil, fmt.Errorf("%w: base dir: %v", ErrSFTPConfig, err)
	}
	if target.ConnectTimeout <= 0 {
		target.ConnectTimeout = sftpDefaultConnectTimeout
	}
	if target.OperationTimeout <= 0 {
		target.OperationTimeout = sftpDefaultOperationTimeout
	}
	store := &SFTPRemoteStore{target: target, dirCached: map[string]struct{}{}}
	copy(store.pin[:], raw)
	return store, nil
}

// HostKeySHA256Hex is the pin format this store expects for a given SSH public key. Tests
// and the operator tooling use it so the stored value and the verification never disagree
// on what exactly is hashed.
func HostKeySHA256Hex(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return hex.EncodeToString(sum[:])
}

// PutVerified implements the atomic completion protocol over SFTP. It returns nil only after
// the remote bytes were read back under the final name (or the final name already held
// exactly that content). It never deletes the local source.
func (s *SFTPRemoteStore) PutVerified(ctx context.Context, job model.ContentBackupJob, lease model.ContentBackupLease, src io.Reader) error {
	if err := s.checkJob(&job, &lease); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sess, err := s.checkoutSession(ctx)
	if err != nil {
		return err
	}
	unwatch := sess.watchOnce(ctx)
	putErr := s.putVerified(ctx, sess, &job, &lease, src)
	unwatch()
	if putErr != nil {
		sess.close()
		return putErr
	}
	s.checkinSession(sess)
	return nil
}

// Close releases idle upload sessions. Open/Remove still use one-shot connections.
func (s *SFTPRemoteStore) Close() error {
	s.sessMu.Lock()
	idle := s.idle
	s.idle = nil
	s.sessMu.Unlock()
	for _, sess := range idle {
		if sess != nil {
			sess.close()
		}
	}
	return nil
}

func (s *SFTPRemoteStore) checkoutSession(ctx context.Context) (*sftpSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.sessMu.Lock()
	for len(s.idle) > 0 {
		sess := s.idle[len(s.idle)-1]
		s.idle = s.idle[:len(s.idle)-1]
		if sess != nil && !sess.dead() && time.Since(sess.lastUsed) <= sftpMaxIdle {
			s.sessMu.Unlock()
			sess.lastUsed = time.Now()
			sess.setDeadline(time.Now().Add(s.target.OperationTimeout))
			return sess, nil
		}
		if sess != nil {
			sess.close()
		}
	}
	s.sessMu.Unlock()
	// 上传会话不挂调用方 ctx：PutVerified 成功后调用方 defer cancel 会把复用连接掐死。
	// 单次超时靠 deadline；失败则丢弃这条会话，不回池。
	return s.dialSession(ctx, time.Now().Add(s.target.OperationTimeout), false)
}

func (s *SFTPRemoteStore) checkinSession(sess *sftpSession) {
	if sess == nil {
		return
	}
	if sess.dead() {
		sess.close()
		return
	}
	sess.lastUsed = time.Now()
	sess.clearDeadline()
	s.sessMu.Lock()
	s.idle = append(s.idle, sess)
	s.sessMu.Unlock()
}

func (s *SFTPRemoteStore) putVerified(ctx context.Context, sess *sftpSession, job *model.ContentBackupJob, lease *model.ContentBackupLease, src io.Reader) (err error) {
	// job.RemotePath is the logical, immutable path recorded in the DB; everything the
	// server sees is the physical path under the session's base directory.
	// Temps live in the incoming shard, including for jobs whose archive path was
	// stored in the old user/session layout. Sweeping that archive directory is what
	// made a nosession folder of tens of thousands of files dominate each upload.
	finalPath := sess.physical(job.RemotePath)
	finalDir := path.Dir(finalPath)
	tempLogical, tempErr := contentbackup.TempRemotePath(s.target.SiteID, job.JobID, lease.Token, sftpTempSuffix)
	if tempErr != nil {
		return tempErr
	}
	tempPath := sess.physical(tempLogical)
	tempDir := path.Dir(tempPath)
	defer func() {
		if err != nil {
			s.dropDirCache(finalDir)
			s.dropDirCache(tempDir)
		}
	}()

	if err = s.ensureDir(ctx, sess, finalDir); err != nil {
		return err
	}
	if err = s.ensureDir(ctx, sess, tempDir); err != nil {
		return err
	}
	// 只有重试才可能在分片里留下上一次租约的临时文件；首次认领（generation 1）
	// 必然扫不到东西，而一次 ReadDir 要 OPENDIR + READDIR + CLOSE 三四个往返。
	if lease.Generation > 1 {
		s.sweepStaleTemps(sess, tempDir, job.JobID, tempPath)
	}

	// 只有重试才可能碰上"上一轮已经改名成功、但 DB 没记上"的最终文件。首次认领不可能，
	// 直接上传；万一真有同名文件，下面的 RENAME 会失败并走同一套存在性校验。
	if lease.Generation > 1 {
		exists, existsErr := s.remoteExists(ctx, sess, finalPath)
		if existsErr != nil {
			err = existsErr
			return err
		}
		if exists {
			var ok bool
			if ok, err = s.verifyRemote(ctx, sess, finalPath, job); err != nil {
				return err
			}
			if !ok {
				err = fmt.Errorf("%w: %s", ErrSFTPConflict, finalPath)
				return err
			}
			return nil
		}
	}

	tracked := &ftpsTrackedReader{r: src}
	if putErr := s.writeTemp(ctx, sess, tempPath, &ftpsThrottledReader{ctx: ctx, limiter: s.target.Limiter, r: tracked}); putErr != nil {
		s.discardTemp(sess, tempPath)
		if tracked.err != nil && tracked.err != io.EOF {
			err = fmt.Errorf("content backup sftp: put: %w (%v)", ErrSFTPLocalSource, tracked.err)
			return err
		}
		err = s.classifyErr(ctx, "put", putErr)
		return err
	}
	if job.CompressedBytes > 0 && tracked.n != job.CompressedBytes {
		s.discardTemp(sess, tempPath)
		err = fmt.Errorf("content backup sftp: put: %w: sent %d of %d bytes", ErrSFTPLocalSource, tracked.n, job.CompressedBytes)
		return err
	}

	ok, err := s.verifyRemote(ctx, sess, tempPath, job)
	if err != nil {
		s.discardTemp(sess, tempPath)
		return err
	}
	if !ok {
		s.discardTemp(sess, tempPath)
		err = fmt.Errorf("%w: %s", ErrSFTPHashMismatch, tempPath)
		return err
	}

	// SSH_FXP_RENAME is defined to fail when the target exists, which is exactly the
	// no-overwrite semantics we need; PosixRename would silently replace and is never used.
	// A refused rename is resolved below by looking at the final name. There used to be a
	// Stat here to shrink the race; it cost one round trip per file (≈240ms cross-border)
	// and added nothing: the final path embeds the job id, so the only possible concurrent
	// writer is this same job carrying the same read-back-verified bytes.
	if renameErr := sess.client.Rename(tempPath, finalPath); renameErr != nil {
		// A refused rename is resolved by looking at the final name, not by guessing:
		// if something now sits there with our content the upload is complete anyway.
		var nowExists bool
		nowExists, err = s.remoteExists(ctx, sess, finalPath)
		if err == nil && nowExists {
			ok, err = s.verifyRemote(ctx, sess, finalPath, job)
			s.discardTemp(sess, tempPath)
			if err != nil {
				return err
			}
			if !ok {
				err = fmt.Errorf("%w: %s", ErrSFTPConflict, finalPath)
				return err
			}
			return nil
		}
		s.discardTemp(sess, tempPath)
		err = s.classifyErr(ctx, "rename", renameErr)
		return err
	}
	return nil
}

// Open streams the immutable remote path for T10 preview/download. Close terminates the
// file handle, the SFTP channel and the SSH connection; cancelling ctx force-closes the
// TCP connection because the libraries' operation methods take no context.
func (s *SFTPRemoteStore) Open(ctx context.Context, job model.ContentBackupJob) (io.ReadCloser, error) {
	if err := s.checkJob(&job, nil); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sess, err := s.dial(ctx, time.Now().Add(s.target.OperationTimeout))
	if err != nil {
		return nil, err
	}
	file, openErr := sess.client.Open(sess.physical(job.RemotePath))
	if openErr != nil {
		sess.close()
		if errors.Is(openErr, os.ErrNotExist) {
			return nil, fmt.Errorf("content backup sftp: open %s: %w (%v)", job.RemotePath, ErrSFTPMissing, openErr)
		}
		return nil, s.classifyErr(ctx, "open", openErr)
	}
	// The download itself is bounded by the caller's ctx and read budget, not by the fixed
	// operation deadline used for the command phase.
	sess.clearDeadline()
	return &sftpRemoteReader{
		sess: sess,
		file: file,
		body: &ftpsThrottledReader{ctx: ctx, limiter: s.target.Limiter, r: file},
	}, nil
}

// ListIncoming lists the temps of one incoming shard on a pooled session, like PutVerified.
func (s *SFTPRemoteStore) ListIncoming(ctx context.Context, shard string) ([]IncomingTemp, error) {
	dir, err := contentbackup.IncomingShardDir(s.target.SiteID, shard)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSFTPInvalidJob, err)
	}
	sess, err := s.checkoutSession(ctx)
	if err != nil {
		return nil, err
	}
	unwatch := sess.watchOnce(ctx)
	temps, listErr := s.listIncoming(ctx, sess, sess.physical(dir))
	unwatch()
	if listErr != nil {
		sess.close()
		return nil, listErr
	}
	s.checkinSession(sess)
	return temps, nil
}

func (s *SFTPRemoteStore) listIncoming(ctx context.Context, sess *sftpSession, dir string) ([]IncomingTemp, error) {
	entries, err := sess.client.ReadDir(dir)
	if err != nil {
		// 分片目录要等第一次上传落到它才建，没建过就是空的。
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, s.classifyErr(ctx, "readdir "+dir, err)
	}
	var temps []IncomingTemp
	for _, entry := range entries {
		if !entry.Mode().IsRegular() {
			continue
		}
		jobID, token, ok := contentbackup.ParseTempName(entry.Name(), sftpTempSuffix)
		if !ok {
			continue
		}
		temps = append(temps, IncomingTemp{JobID: jobID, Token: token, ModTime: sftpEntryModTime(entry)})
	}
	return temps, nil
}

// sftpEntryModTime maps a missing mtime, which pkg/sftp reports as the Unix epoch rather
// than the zero time, to zero: the reaper then treats the temp's age as unknown.
func sftpEntryModTime(entry os.FileInfo) time.Time {
	if modTime := entry.ModTime(); modTime.Unix() > 0 {
		return modTime
	}
	return time.Time{}
}

// RemoveIncoming deletes one temp by the name TempRemotePath rebuilds from its job and
// token, never by a listed raw name.
func (s *SFTPRemoteStore) RemoveIncoming(ctx context.Context, temp IncomingTemp) error {
	if !ftpsLeaseTokenPattern.MatchString(temp.Token) {
		return fmt.Errorf("%w: lease token is not a safe file name component", ErrSFTPInvalidJob)
	}
	tempLogical, err := contentbackup.TempRemotePath(s.target.SiteID, temp.JobID, temp.Token, sftpTempSuffix)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSFTPInvalidJob, err)
	}
	sess, err := s.checkoutSession(ctx)
	if err != nil {
		return err
	}
	unwatch := sess.watchOnce(ctx)
	removeErr := s.removeIncoming(ctx, sess, sess.physical(tempLogical))
	unwatch()
	if removeErr != nil {
		sess.close()
		return removeErr
	}
	s.checkinSession(sess)
	return nil
}

func (s *SFTPRemoteStore) removeIncoming(ctx context.Context, sess *sftpSession, tempPath string) error {
	err := sess.client.Remove(tempPath)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return s.classifyErr(ctx, "remove "+tempPath, err)
}

// Remove deletes the job's final remote object. Only the connection probe uses it.
func (s *SFTPRemoteStore) Remove(ctx context.Context, job model.ContentBackupJob) error {
	if err := s.checkJob(&job, nil); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sess, err := s.dial(ctx, time.Now().Add(s.target.OperationTimeout))
	if err != nil {
		return err
	}
	defer sess.close()
	if delErr := sess.client.Remove(sess.physical(job.RemotePath)); delErr != nil {
		if errors.Is(delErr, os.ErrNotExist) {
			return fmt.Errorf("content backup sftp: remove %s: %w (%v)", job.RemotePath, ErrSFTPMissing, delErr)
		}
		return s.classifyErr(ctx, "remove", delErr)
	}
	return nil
}

func (s *SFTPRemoteStore) checkJob(job *model.ContentBackupJob, lease *model.ContentBackupLease) error {
	if job.TargetID != s.target.TargetID {
		return fmt.Errorf("%w: job target_id %q is not the configured target %q", ErrSFTPInvalidJob, job.TargetID, s.target.TargetID)
	}
	if err := contentbackup.ValidateJobID(job.JobID); err != nil {
		return fmt.Errorf("%w: %v", ErrSFTPInvalidJob, err)
	}
	clean := path.Clean(job.RemotePath)
	if job.RemotePath == "" || clean != job.RemotePath || strings.Contains(clean, "..") ||
		!strings.HasPrefix(clean, "/"+s.target.SiteID+"/") {
		return fmt.Errorf("%w: remote_path %q is not an immutable canonical path under site %q",
			ErrSFTPInvalidJob, job.RemotePath, s.target.SiteID)
	}
	if path.Base(clean) != job.JobID+contentbackup.RemoteFileExtension {
		return fmt.Errorf("%w: remote_path base must be job_id + %s", ErrSFTPInvalidJob, contentbackup.RemoteFileExtension)
	}
	if !ftpsIsLowerHex64(job.CompressedSHA256) {
		return fmt.Errorf("%w: compressed_sha256 must be 64 lowercase hex characters", ErrSFTPInvalidJob)
	}
	if lease != nil {
		if lease.JobID != job.JobID {
			return fmt.Errorf("%w: lease job_id %q does not match job %q", ErrSFTPInvalidJob, lease.JobID, job.JobID)
		}
		if !ftpsLeaseTokenPattern.MatchString(lease.Token) {
			return fmt.Errorf("%w: lease token is not a safe file name component", ErrSFTPInvalidJob)
		}
	}
	return nil
}

// ensureDir creates every path segment, caching verified directories per store instance
// (design doc 6.3 step 3). A failed mkdir is never assumed to mean "exists": only a
// successful stat that reports a directory proves the path is usable.
func (s *SFTPRemoteStore) ensureDir(ctx context.Context, sess *sftpSession, dir string) error {
	s.dirMu.Lock()
	_, cached := s.dirCached[dir]
	s.dirMu.Unlock()
	if cached {
		return nil
	}
	current := ""
	for _, segment := range strings.Split(strings.Trim(dir, "/"), "/") {
		current += "/" + segment
		if mkErr := sess.client.Mkdir(current); mkErr != nil {
			info, statErr := sess.client.Stat(current)
			if statErr != nil || !info.IsDir() {
				return s.classifyErr(ctx, "mkdir "+current, mkErr)
			}
		}
	}
	s.dirMu.Lock()
	s.dirCached[dir] = struct{}{}
	s.dirMu.Unlock()
	return nil
}

func (s *SFTPRemoteStore) dropDirCache(dir string) {
	s.dirMu.Lock()
	delete(s.dirCached, dir)
	s.dirMu.Unlock()
}

// sweepStaleTemps only deletes temp files of this exact job whose lease token differs from
// the current one (design doc 6.3 step 6). dir is the incoming shard, never the archive
// directory; no wildcard deletion.
func (s *SFTPRemoteStore) sweepStaleTemps(sess *sftpSession, dir, jobID, currentTemp string) {
	entries, err := sess.client.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := jobID + "."
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, sftpTempSuffix) {
			continue
		}
		stale := dir + "/" + name
		if stale == currentTemp {
			continue
		}
		_ = sess.client.Remove(stale)
	}
}

func (s *SFTPRemoteStore) remoteExists(ctx context.Context, sess *sftpSession, remotePath string) (bool, error) {
	info, err := sess.client.Stat(remotePath)
	if err == nil {
		if info.IsDir() {
			return false, fmt.Errorf("content backup sftp: stat %s: %w (a directory sits at the final path)", remotePath, ErrSFTPConflict)
		}
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, s.classifyErr(ctx, "stat", err)
}

func (s *SFTPRemoteStore) writeTemp(ctx context.Context, sess *sftpSession, tempPath string, src io.Reader) (err error) {
	file, openErr := sess.client.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if openErr != nil {
		return openErr
	}
	defer func() {
		// Close flushes and releases the remote handle; a failure there means the bytes
		// are not known to be on disk, so it downgrades an otherwise clean copy.
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	_, err = io.Copy(file, src)
	return err
}

func (s *SFTPRemoteStore) verifyRemote(ctx context.Context, sess *sftpSession, remotePath string, job *model.ContentBackupJob) (bool, error) {
	file, openErr := sess.client.Open(remotePath)
	if openErr != nil {
		if errors.Is(openErr, os.ErrNotExist) {
			return false, fmt.Errorf("content backup sftp: open %s: %w (%v)", remotePath, ErrSFTPMissing, openErr)
		}
		return false, s.classifyErr(ctx, "open", openErr)
	}
	defer file.Close()
	hasher := sha256.New()
	n, copyErr := io.Copy(hasher, &ftpsThrottledReader{ctx: ctx, limiter: s.target.Limiter, r: file})
	if copyErr != nil {
		return false, s.classifyErr(ctx, "read", copyErr)
	}
	if job.CompressedBytes > 0 && n != job.CompressedBytes {
		return false, nil
	}
	return hex.EncodeToString(hasher.Sum(nil)) == job.CompressedSHA256, nil
}

func (s *SFTPRemoteStore) discardTemp(sess *sftpSession, tempPath string) {
	_ = sess.client.Remove(tempPath)
}

// dial opens the TCP connection, runs the SSH handshake against the pinned host key and
// starts the sftp subsystem. The host key is checked before any authentication method
// runs, so a wrong pin never sends the password anywhere.
func (s *SFTPRemoteStore) dial(ctx context.Context, phaseDeadline time.Time) (*sftpSession, error) {
	return s.dialSession(ctx, phaseDeadline, true)
}

func (s *SFTPRemoteStore) dialSession(ctx context.Context, phaseDeadline time.Time, watch bool) (*sftpSession, error) {
	addr := net.JoinHostPort(s.target.Host, strconv.Itoa(s.target.Port))
	dialer := net.Dialer{Timeout: s.target.ConnectTimeout}
	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, s.classifyErr(ctx, "connect", err)
	}
	sess := newSFTPSession(raw)
	if watch {
		sess.watch(ctx)
	}
	_ = raw.SetDeadline(phaseDeadline)

	pin := s.pin
	hostKeyCallback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if sum := sha256.Sum256(key.Marshal()); sum != pin {
			return fmt.Errorf("%w: server offered %s key %s (%s), pinned %s",
				ErrSFTPHostKey, key.Type(), hex.EncodeToString(sum[:]), ssh.FingerprintSHA256(key), hex.EncodeToString(pin[:]))
		}
		return nil
	}
	password := s.target.Password
	clientConfig := &ssh.ClientConfig{
		User: s.target.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
			// Servers that only advertise keyboard-interactive (PAM) still want the same
			// password; answer every prompt with it and nothing else.
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = password
				}
				return answers, nil
			}),
		},
		HostKeyCallback: hostKeyCallback,
		// The server presents the first key type in *our* preference list that it has, so
		// this order decides which key the operator must pin. x/crypto's default prefers
		// ECDSA over Ed25519; we fix Ed25519 first (what the UI tells operators to scan),
		// then ECDSA, then RSA with SHA-2 only. No ssh-rsa/SHA-1, no DSA, no certificates.
		HostKeyAlgorithms: sftpHostKeyAlgorithms,
		Timeout:           s.target.ConnectTimeout,
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(raw, addr, clientConfig)
	if err != nil {
		sess.close()
		return nil, s.classifyErr(ctx, "handshake", err)
	}
	sess.ssh = ssh.NewClient(sshConn, chans, reqs)
	client, err := sftp.NewClient(sess.ssh)
	if err != nil {
		sess.close()
		return nil, s.classifyErr(ctx, "subsystem", err)
	}
	sess.client = client
	sess.lastUsed = time.Now()
	sess.startKeepalive()
	sess.base = s.target.BaseDir
	if sess.base == "" {
		// Not chrooted: "/" is the real root. Anchor the logical tree at the directory the
		// server puts this account in, asked once per session so a moved home is noticed.
		home, homeErr := client.Getwd()
		if homeErr != nil {
			sess.close()
			return nil, s.classifyErr(ctx, "realpath", homeErr)
		}
		if err := contentbackup.ValidateRemoteBaseDir(home); err != nil || home == "" {
			sess.close()
			return nil, fmt.Errorf("content backup sftp: realpath: %w (server reported login directory %q: %v)", ErrSFTPUnsupported, home, err)
		}
		sess.base = home
	}
	return sess, nil
}

// classifyErr maps transport failures onto the sentinel classes T05 consumes. The
// underlying error is always attached with %v, never %w: pkg/sftp normalises "no such
// file" to os.ErrNotExist, and the upload classifier treats a bare os.ErrNotExist as the
// *local* backup being gone, which would wrongly make the job unrecoverable.
func (s *SFTPRemoteStore) classifyErr(ctx context.Context, phase string, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("content backup sftp: %s aborted: %w (%v)", phase, ctxErr, err)
	}
	return fmt.Errorf("content backup sftp: %s: %w (%v)", phase, sftpSentinelFor(phase, err), err)
}

func sftpSentinelFor(phase string, err error) error {
	if errors.Is(err, ErrSFTPHostKey) {
		return ErrSFTPHostKey
	}
	var authErr *ssh.ServerAuthError
	if errors.As(err, &authErr) || strings.Contains(err.Error(), "unable to authenticate") {
		return ErrSFTPAuth
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrSFTPTimeout
	}
	switch phase {
	case "connect", "handshake":
		// Algorithm negotiation or protocol failures at the SSH layer: the server is
		// reachable but cannot be spoken to safely. Never downgrade; treat as unusable.
		if strings.Contains(err.Error(), "no common algorithm") || strings.Contains(err.Error(), "unsupported") {
			return ErrSFTPUnsupported
		}
		return ErrSFTPNetwork
	case "subsystem":
		return ErrSFTPUnsupported
	}
	if errors.Is(err, os.ErrPermission) {
		return ErrSFTPPermission
	}
	if errors.Is(err, os.ErrNotExist) {
		// Outside the explicit open/stat paths a vanished parent means the server refused
		// or lost our directory; the job is not gone locally, so this must stay a remote
		// class, never os.ErrNotExist itself.
		return ErrSFTPPermission
	}
	var status *sftp.StatusError
	if errors.As(err, &status) {
		switch status.Code {
		case 8: // SSH_FX_OP_UNSUPPORTED
			return ErrSFTPUnsupported
		case 6, 7: // SSH_FX_NO_CONNECTION, SSH_FX_CONNECTION_LOST
			return ErrSFTPNetwork
		default: // SSH_FX_FAILURE and friends: quota, exists, server side refusal
			return ErrSFTPPermission
		}
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return ErrSFTPNetwork
	}
	return ErrSFTPNetwork
}

// sftpSession owns one TCP connection and the SSH/SFTP layers on top of it. The libraries'
// operations take no context, so cancellation force-closes the TCP connection, which makes
// any blocked transfer return an error.
type sftpSession struct {
	raw    net.Conn
	ssh    *ssh.Client
	client *sftp.Client
	// base is the resolved physical directory for this session (see SFTPTarget.BaseDir).
	base     string
	lastUsed time.Time

	mu       sync.Mutex
	aborted  bool
	stop     chan struct{}
	stopOnce sync.Once
}

func (s *sftpSession) startKeepalive() {
	if s == nil || s.ssh == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(sftpKeepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				_, _, err := s.ssh.SendRequest("keepalive@openssh.com", true, nil)
				if err != nil {
					s.abort()
					return
				}
			}
		}
	}()
}

func newSFTPSession(raw net.Conn) *sftpSession {
	return &sftpSession{raw: raw, stop: make(chan struct{})}
}

// physical maps a validated logical remote_path ("/{site}/...") onto the server path.
// checkJob has already rejected anything that is not a clean absolute path without "..",
// so a plain join cannot escape the base directory.
func (s *sftpSession) physical(logical string) string {
	if s.base == "" || s.base == "/" {
		return logical
	}
	return path.Join(s.base, logical)
}

func (s *sftpSession) watch(ctx context.Context) {
	s.watchOnce(ctx)
}

// watchOnce aborts the TCP connection if ctx is cancelled. The returned function
// detaches the watch so a later cancel (typical: caller defer cancel after success)
// cannot kill a session that is going back to the pool.
func (s *sftpSession) watchOnce(ctx context.Context) func() {
	done := make(chan struct{})
	var once sync.Once
	unwatch := func() { once.Do(func() { close(done) }) }
	go func() {
		select {
		case <-ctx.Done():
			s.abort()
		case <-done:
		case <-s.stop:
		}
	}()
	return unwatch
}

func (s *sftpSession) dead() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aborted
}

func (s *sftpSession) abort() {
	s.mu.Lock()
	already := s.aborted
	s.aborted = true
	s.mu.Unlock()
	if already {
		return
	}
	_ = s.raw.Close()
}

func (s *sftpSession) setDeadline(t time.Time) {
	_ = s.raw.SetDeadline(t)
}

func (s *sftpSession) clearDeadline() {
	s.setDeadline(time.Time{})
}

// close tears the layers down in order and is safe to call more than once. A short
// deadline bounds the polite shutdown so a dead peer cannot hang the worker.
func (s *sftpSession) close() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.setDeadline(time.Now().Add(sftpShutdownGrace))
	if s.client != nil {
		_ = s.client.Close()
	}
	if s.ssh != nil {
		_ = s.ssh.Close()
	}
	s.abort()
}

type sftpRemoteReader struct {
	sess      *sftpSession
	file      *sftp.File
	body      io.Reader
	closeOnce sync.Once
	closeErr  error
}

func (r *sftpRemoteReader) Read(p []byte) (int, error) {
	return r.body.Read(p)
}

// Close terminates the file handle, the SFTP channel and the SSH connection (design doc
// 9.2) and is safe to call more than once.
func (r *sftpRemoteReader) Close() error {
	r.closeOnce.Do(func() {
		r.sess.setDeadline(time.Now().Add(sftpShutdownGrace))
		fileErr := r.file.Close()
		r.sess.close()
		r.closeErr = fileErr
	})
	return r.closeErr
}
