package contentbackupworker

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/contentbackup"
	"github.com/jlaffaye/ftp"
)

// RemoteStore is the frozen transfer contract from design doc 9.2. Both
// methods own the connection lifetime; T05/T10 inject fakes instead of the
// concrete FTPS implementation.
type RemoteStore interface {
	PutVerified(ctx context.Context, job model.ContentBackupJob, lease model.ContentBackupLease, src io.Reader) error
	Open(ctx context.Context, job model.ContentBackupJob) (io.ReadCloser, error)
	// Remove deletes the job's final remote object. The connection probe needs it
	// so it can report a truthful delete stage instead of a hardcoded green check.
	Remove(ctx context.Context, job model.ContentBackupJob) error
	// ListIncoming lists the temp objects PutVerified writes in one incoming shard
	// ("00".."ff"); names this code could not have written are left out. A shard that
	// was never created lists as empty.
	ListIncoming(ctx context.Context, shard string) ([]IncomingTemp, error)
	// RemoveIncoming deletes one temp object; one that is already gone is not an error.
	RemoveIncoming(ctx context.Context, temp IncomingTemp) error
}

// IncomingTemp is one temp object found in an incoming shard. ModTime comes from the
// server's clock and is zero when the listing carried none.
type IncomingTemp struct {
	JobID   string
	Token   string
	ModTime time.Time
}

var _ RemoteStore = (*FTPSRemoteStore)(nil)

var (
	ErrFTPSConfig       = errors.New("content backup ftps: target configuration is unusable")
	ErrFTPSInvalidJob   = errors.New("content backup ftps: job or lease identity does not match the configured target")
	ErrFTPSInvalidPin   = errors.New("content backup ftps: cert_sha256 must be 64 lowercase hex characters")
	ErrFTPSCertificate  = errors.New("content backup ftps: certificate pin or TLS channel protection failed")
	ErrFTPSAuth         = errors.New("content backup ftps: server rejected the credentials")
	ErrFTPSPermission   = errors.New("content backup ftps: server denied the operation")
	ErrFTPSConflict     = errors.New("content backup ftps: remote_conflict: final remote file exists with different content")
	ErrFTPSHashMismatch = errors.New("content backup ftps: read-back digest does not match compressed_sha256")
	ErrFTPSLocalSource  = errors.New("content backup ftps: local source stream failed or has the wrong length")
	ErrFTPSMissing      = errors.New("content backup ftps: remote file is missing")
	ErrFTPSTimeout      = errors.New("content backup ftps: operation timed out")
	ErrFTPSNetwork      = errors.New("content backup ftps: retryable network failure")
	ErrFTPSUnsupported  = errors.New("content backup ftps: remote lacks the required rename/duplicate-name semantics")
)

const (
	ftpsTempSuffix    = ".uploading"
	ftpsShutdownGrace = 15 * time.Second

	ftpsDefaultConnectTimeout   = 10 * time.Second
	ftpsDefaultOperationTimeout = 120 * time.Second
	// ftpsMaxIdle 比常见 FTP 服务端的 300s 空闲断连保守得多：与其发 NOOP 保活，
	// 不如让取出时超龄的会话直接重拨，省掉"拿到一条已被服务端悄悄关掉的连接"。
	ftpsMaxIdle = 60 * time.Second
)

var ftpsLeaseTokenPattern = contentbackup.LeaseTokenPattern

// FTPSTarget is the immutable target identity (design doc 6.3 step 1 and 7.1):
// host/port, account root identity and site_label are bound together with the
// target_id; credentials come from deployment variables, never from the web
// config. Limiter may be nil (unlimited); upload workers share one limiter so
// STOR and the verification RETR draw from the same node-wide byte budget.
type FTPSTarget struct {
	TargetID         string
	SiteID           string
	Host             string
	Port             int
	Username         string
	Password         string
	CertSHA256       string
	ConnectTimeout   time.Duration
	OperationTimeout time.Duration
	Limiter          *BandwidthLimiter
}

// FTPSRemoteStore speaks explicit FTPS (AUTH TLS on port 21 style) and
// implements the atomic completion protocol from design doc 6.3: STOR to a
// job+lease-token temp name, streaming RETR digest verification, then
// RNFR/RNTO. It never overwrites an existing final file and never deletes the
// local source (that is T05/T06 territory).
type FTPSRemoteStore struct {
	target    FTPSTarget
	pin       [sha256.Size]byte
	dirMu     sync.Mutex
	dirCached map[string]struct{}
	// tlsCache 让数据通道走 TLS 恢复：每个文件要新建 STOR/RETR 两条数据连接，
	// 完整握手各要 2 个 RTT。它同时满足 vsftpd 默认的 require_ssl_reuse。
	tlsCache tls.ClientSessionCache

	sessMu sync.Mutex
	idle   []*ftpsSession
}

func NewFTPSRemoteStore(target FTPSTarget) (*FTPSRemoteStore, error) {
	// 构造期的校验失败全部挂 ErrFTPSConfig：它们描述的是"目标配置本身不可用"，
	// 退避重试 16 次不会让配置自己变好，必须落 failed 并暂停目标（文档 6.2）。
	if target.TargetID == "" {
		return nil, fmt.Errorf("%w: target_id is required", ErrFTPSConfig)
	}
	if err := contentbackup.ValidateSiteLabel(target.SiteID); err != nil {
		return nil, fmt.Errorf("%w: invalid site label: %v", ErrFTPSConfig, err)
	}
	if target.Host == "" {
		return nil, fmt.Errorf("%w: host is required", ErrFTPSConfig)
	}
	if target.Port < 1 || target.Port > 65535 {
		return nil, fmt.Errorf("%w: port out of range: %d", ErrFTPSConfig, target.Port)
	}
	if target.Username == "" {
		return nil, fmt.Errorf("%w: username is required", ErrFTPSConfig)
	}
	if len(target.CertSHA256) != 64 {
		return nil, fmt.Errorf("%w: got %d characters", ErrFTPSInvalidPin, len(target.CertSHA256))
	}
	raw, err := hex.DecodeString(target.CertSHA256)
	if err != nil || strings.ToLower(target.CertSHA256) != target.CertSHA256 {
		return nil, fmt.Errorf("%w: %v", ErrFTPSInvalidPin, err)
	}
	if target.ConnectTimeout <= 0 {
		target.ConnectTimeout = ftpsDefaultConnectTimeout
	}
	if target.OperationTimeout <= 0 {
		target.OperationTimeout = ftpsDefaultOperationTimeout
	}
	store := &FTPSRemoteStore{
		target:    target,
		dirCached: map[string]struct{}{},
		tlsCache:  tls.NewLRUClientSessionCache(32),
	}
	copy(store.pin[:], raw)
	return store, nil
}

// PutVerified uploads src to job.RemotePath with the atomic completion
// protocol. It returns nil only after the remote bytes have been read back
// and matched job.CompressedSHA256 under the final name (or the final name
// already held exactly that content). The caller must not delete the local
// file before the DB commit; this method never touches local state.
func (s *FTPSRemoteStore) PutVerified(ctx context.Context, job model.ContentBackupJob, lease model.ContentBackupLease, src io.Reader) error {
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
		// 失败的会话不回池：控制连接可能已经停在半个命令上。
		sess.close()
		return putErr
	}
	s.checkinSession(sess)
	return nil
}

// Close releases idle upload sessions. Open/Remove still use one-shot connections.
func (s *FTPSRemoteStore) Close() error {
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

// checkoutSession 借一条已登录的控制连接。以前每个文件都要重付 TCP + AUTH TLS +
// USER/PASS + FEAT/TYPE/PBSZ/PROT 这一整套，约 12 个往返，占单文件开销的三分之一。
func (s *FTPSRemoteStore) checkoutSession(ctx context.Context) (*ftpsSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var (
		reuse *ftpsSession
		stale []*ftpsSession
	)
	s.sessMu.Lock()
	for len(s.idle) > 0 {
		sess := s.idle[len(s.idle)-1]
		s.idle = s.idle[:len(s.idle)-1]
		if sess == nil {
			continue
		}
		if !sess.dead() && time.Since(sess.lastUsed) <= ftpsMaxIdle {
			reuse = sess
			break
		}
		stale = append(stale, sess)
	}
	s.sessMu.Unlock()
	// QUIT 是一次真实往返，不能拿着池锁做，否则所有 worker 会在这里排成一列。
	for _, sess := range stale {
		go sess.close()
	}
	if reuse != nil {
		reuse.lastUsed = time.Now()
		reuse.setDeadline(time.Now().Add(s.target.OperationTimeout))
		return reuse, nil
	}
	// 上传会话不挂调用方 ctx：PutVerified 成功后调用方 defer cancel 会把复用连接掐死。
	// 单次超时靠 deadline；失败则丢弃这条会话，不回池。
	return s.dialSession(ctx, time.Now().Add(s.target.OperationTimeout), false)
}

func (s *FTPSRemoteStore) checkinSession(sess *ftpsSession) {
	if sess == nil {
		return
	}
	sess.releaseDataConns()
	if sess.dead() {
		sess.close()
		return
	}
	sess.lastUsed = time.Now()
	sess.clearDeadlines()
	s.sessMu.Lock()
	s.idle = append(s.idle, sess)
	s.sessMu.Unlock()
}

func (s *FTPSRemoteStore) putVerified(ctx context.Context, sess *ftpsSession, job *model.ContentBackupJob, lease *model.ContentBackupLease, src io.Reader) (err error) {
	finalPath := job.RemotePath
	finalDir := path.Dir(finalPath)
	// Temps stay in the incoming shard even when the stored archive path still uses
	// the old user/session layout, so a sweep never lists the archive directory.
	tempPath, tempErr := contentbackup.TempRemotePath(s.target.SiteID, job.JobID, lease.Token, ftpsTempSuffix)
	if tempErr != nil {
		return tempErr
	}
	tempDir := path.Dir(tempPath)
	defer func() {
		// Any failure invalidates the directory cache so the next attempt
		// re-confirms the directory is really usable.
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
	// 必然扫不到东西，而一次 LIST 在 FTPS 上是一整条数据连接加一次 TLS 握手。
	if lease.Generation > 1 {
		s.sweepStaleTemps(sess, tempDir, job.JobID, tempPath)
	}

	exists, err := s.remoteExists(ctx, sess.conn, finalPath)
	if err != nil {
		return err
	}
	if exists {
		var ok bool
		if ok, err = s.verifyRemote(ctx, sess, finalPath, job); err != nil {
			return err
		}
		if !ok {
			err = fmt.Errorf("%w: %s", ErrFTPSConflict, finalPath)
			return err
		}
		return nil
	}

	tracked := &ftpsTrackedReader{r: src}
	if storErr := sess.conn.Stor(tempPath, &ftpsThrottledReader{ctx: ctx, limiter: s.target.Limiter, r: tracked}); storErr != nil {
		s.discardTemp(sess, tempPath)
		if tracked.err != nil && tracked.err != io.EOF {
			err = fmt.Errorf("content backup ftps: stor: %w (%v)", ErrFTPSLocalSource, tracked.err)
			return err
		}
		err = s.classifyErr(ctx, "stor", storErr)
		return err
	}
	if job.CompressedBytes > 0 && tracked.n != job.CompressedBytes {
		s.discardTemp(sess, tempPath)
		err = fmt.Errorf("content backup ftps: stor: %w: sent %d of %d bytes", ErrFTPSLocalSource, tracked.n, job.CompressedBytes)
		return err
	}

	ok, err := s.verifyRemote(ctx, sess, tempPath, job)
	if err != nil {
		s.discardTemp(sess, tempPath)
		return err
	}
	if !ok {
		s.discardTemp(sess, tempPath)
		err = fmt.Errorf("%w: %s", ErrFTPSHashMismatch, tempPath)
		return err
	}

	// FTP has no compare-and-swap rename, so re-check the final name right
	// before RNTO to shrink (not eliminate) the race against a concurrent
	// worker; the immutable per-job content bounds the residual window.
	exists, err = s.remoteExists(ctx, sess.conn, finalPath)
	if err != nil {
		s.discardTemp(sess, tempPath)
		return err
	}
	if exists {
		ok, err = s.verifyRemote(ctx, sess, finalPath, job)
		s.discardTemp(sess, tempPath)
		if err != nil {
			return err
		}
		if !ok {
			err = fmt.Errorf("%w: %s", ErrFTPSConflict, finalPath)
			return err
		}
		return nil
	}

	if renameErr := sess.conn.Rename(tempPath, finalPath); renameErr != nil {
		code, proto := ftpsTextCode(renameErr)
		if proto && (code == ftp.StatusFileUnavailable || code == ftp.StatusExceededStorage || code == ftp.StatusBadFileName) {
			// Some servers refuse RNTO when the target exists; resolve that by
			// read-back instead of assuming the rename failed outright.
			var nowExists bool
			nowExists, err = s.remoteExists(ctx, sess.conn, finalPath)
			if err == nil && nowExists {
				ok, err = s.verifyRemote(ctx, sess, finalPath, job)
				s.discardTemp(sess, tempPath)
				if err != nil {
					return err
				}
				if !ok {
					err = fmt.Errorf("%w: %s", ErrFTPSConflict, finalPath)
					return err
				}
				return nil
			}
		}
		s.discardTemp(sess, tempPath)
		err = s.classifyErr(ctx, "rename", renameErr)
		return err
	}
	return nil
}

// Open starts a RETR of the immutable remote path for T10 preview/download.
// The returned ReadCloser must be closed; Close terminates both the data and
// the control connection. Streaming lifetime is bounded by ctx: cancelling it
// force-closes both connections because the library's operation methods take
// no context.
func (s *FTPSRemoteStore) Open(ctx context.Context, job model.ContentBackupJob) (io.ReadCloser, error) {
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
	resp, retrErr := sess.conn.Retr(job.RemotePath)
	if retrErr != nil {
		sess.close()
		if code, ok := ftpsTextCode(retrErr); ok && code == ftp.StatusFileUnavailable {
			return nil, fmt.Errorf("content backup ftps: retr %s: %w (%v)", job.RemotePath, ErrFTPSMissing, retrErr)
		}
		return nil, s.classifyErr(ctx, "retr", retrErr)
	}
	// The download itself is bounded by the caller's ctx and read budget, not
	// by the fixed operation deadline used for the command phase.
	sess.clearDeadlines()
	return &ftpsRemoteReader{
		sess: sess,
		resp: resp,
		body: &ftpsThrottledReader{ctx: ctx, limiter: s.target.Limiter, r: resp},
	}, nil
}

// ListIncoming lists the temps of one incoming shard on a pooled session, like PutVerified.
func (s *FTPSRemoteStore) ListIncoming(ctx context.Context, shard string) ([]IncomingTemp, error) {
	dir, err := contentbackup.IncomingShardDir(s.target.SiteID, shard)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFTPSInvalidJob, err)
	}
	sess, err := s.checkoutSession(ctx)
	if err != nil {
		return nil, err
	}
	unwatch := sess.watchOnce(ctx)
	temps, listErr := s.listIncoming(ctx, sess, dir)
	unwatch()
	if listErr != nil {
		sess.close()
		return nil, listErr
	}
	s.checkinSession(sess)
	return temps, nil
}

func (s *FTPSRemoteStore) listIncoming(ctx context.Context, sess *ftpsSession, dir string) ([]IncomingTemp, error) {
	entries, err := sess.conn.List(dir)
	if err != nil {
		// LIST 550：分片目录要等第一次上传落到它才建，没建过就是空的。
		if code, ok := ftpsTextCode(err); ok && code == ftp.StatusFileUnavailable {
			return nil, nil
		}
		return nil, s.classifyErr(ctx, "list "+dir, err)
	}
	// Only MLSD times are exact UTC. Without it the library reads LIST times as UTC in the
	// current year: a server printing local time is off by hours, and around New Year a
	// fresh temp reads as a year old. Such temps come back undated and are never reaped.
	precise := sess.conn.IsTimePreciseInList()
	var temps []IncomingTemp
	for _, entry := range entries {
		if entry.Type != ftp.EntryTypeFile {
			continue
		}
		jobID, token, ok := contentbackup.ParseTempName(entry.Name, ftpsTempSuffix)
		if !ok {
			continue
		}
		temp := IncomingTemp{JobID: jobID, Token: token}
		if precise {
			temp.ModTime = entry.Time
		}
		temps = append(temps, temp)
	}
	return temps, nil
}

// RemoveIncoming deletes one temp by the name TempRemotePath rebuilds from its job and
// token, never by a listed raw name.
func (s *FTPSRemoteStore) RemoveIncoming(ctx context.Context, temp IncomingTemp) error {
	if !ftpsLeaseTokenPattern.MatchString(temp.Token) {
		return fmt.Errorf("%w: lease token is not a safe file name component", ErrFTPSInvalidJob)
	}
	tempPath, err := contentbackup.TempRemotePath(s.target.SiteID, temp.JobID, temp.Token, ftpsTempSuffix)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrFTPSInvalidJob, err)
	}
	sess, err := s.checkoutSession(ctx)
	if err != nil {
		return err
	}
	unwatch := sess.watchOnce(ctx)
	removeErr := s.removeIncoming(ctx, sess, tempPath)
	unwatch()
	if removeErr != nil {
		sess.close()
		return removeErr
	}
	s.checkinSession(sess)
	return nil
}

func (s *FTPSRemoteStore) removeIncoming(ctx context.Context, sess *ftpsSession, tempPath string) error {
	err := sess.conn.Delete(tempPath)
	if err == nil {
		return nil
	}
	// FTP 的 550 分不清"不存在"和"被拒绝"；真没权限的话，同目录的上传早就失败了。
	if code, ok := ftpsTextCode(err); ok && code == ftp.StatusFileUnavailable {
		return nil
	}
	return s.classifyErr(ctx, "dele", err)
}

// Remove deletes the job's final remote object. Only the connection probe uses it:
// the daemon never deletes real archives, cleanup only reclaims the local spool.
func (s *FTPSRemoteStore) Remove(ctx context.Context, job model.ContentBackupJob) error {
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
	if delErr := sess.conn.Delete(job.RemotePath); delErr != nil {
		return s.classifyErr(ctx, "dele", delErr)
	}
	return nil
}

func (s *FTPSRemoteStore) checkJob(job *model.ContentBackupJob, lease *model.ContentBackupLease) error {
	if job.TargetID != s.target.TargetID {
		return fmt.Errorf("%w: job target_id %q is not the configured target %q", ErrFTPSInvalidJob, job.TargetID, s.target.TargetID)
	}
	if err := contentbackup.ValidateJobID(job.JobID); err != nil {
		return fmt.Errorf("%w: %v", ErrFTPSInvalidJob, err)
	}
	clean := path.Clean(job.RemotePath)
	if job.RemotePath == "" || clean != job.RemotePath || strings.Contains(clean, "..") ||
		!strings.HasPrefix(clean, "/"+s.target.SiteID+"/") {
		return fmt.Errorf("%w: remote_path %q is not an immutable canonical path under site %q",
			ErrFTPSInvalidJob, job.RemotePath, s.target.SiteID)
	}
	if path.Base(clean) != job.JobID+contentbackup.RemoteFileExtension {
		return fmt.Errorf("%w: remote_path base must be job_id + %s", ErrFTPSInvalidJob, contentbackup.RemoteFileExtension)
	}
	if !ftpsIsLowerHex64(job.CompressedSHA256) {
		return fmt.Errorf("%w: compressed_sha256 must be 64 lowercase hex characters", ErrFTPSInvalidJob)
	}
	if lease != nil {
		if lease.JobID != job.JobID {
			return fmt.Errorf("%w: lease job_id %q does not match job %q", ErrFTPSInvalidJob, lease.JobID, job.JobID)
		}
		if !ftpsLeaseTokenPattern.MatchString(lease.Token) {
			return fmt.Errorf("%w: lease token is not a safe file name component", ErrFTPSInvalidJob)
		}
	}
	return nil
}

// ensureDir creates every path segment, caching verified directories on this
// store instance to cut down MKD traffic (design doc 6.3 step 3).
func (s *FTPSRemoteStore) ensureDir(ctx context.Context, sess *ftpsSession, dir string) error {
	s.dirMu.Lock()
	_, cached := s.dirCached[dir]
	s.dirMu.Unlock()
	if cached {
		return nil
	}
	current := ""
	for _, segment := range strings.Split(strings.Trim(dir, "/"), "/") {
		current += "/" + segment
		if mkdErr := sess.conn.MakeDir(current); mkdErr != nil {
			// MKD 550 is ambiguous (exists / denied / quota); only a successful
			// CWD proves the directory really is usable, so never treat a bare
			// 550 as "already exists".
			if cdErr := sess.conn.ChangeDir(current); cdErr != nil {
				return s.classifyErr(ctx, "mkdir "+current, mkdErr)
			}
		}
	}
	s.dirMu.Lock()
	s.dirCached[dir] = struct{}{}
	s.dirMu.Unlock()
	return nil
}

func (s *FTPSRemoteStore) dropDirCache(dir string) {
	s.dirMu.Lock()
	delete(s.dirCached, dir)
	s.dirMu.Unlock()
}

// sweepStaleTemps only ever deletes temp files of this exact job whose lease
// token differs from the current one (design doc 6.3 step 6). dir is the incoming
// shard; other jobs' temps may still belong to a live lease, so no wildcard deletion.
func (s *FTPSRemoteStore) sweepStaleTemps(sess *ftpsSession, dir, jobID, currentTemp string) {
	entries, err := sess.conn.List(dir)
	if err != nil {
		return
	}
	prefix := jobID + "."
	for _, entry := range entries {
		if entry.Type != ftp.EntryTypeFile {
			continue
		}
		if !strings.HasPrefix(entry.Name, prefix) || !strings.HasSuffix(entry.Name, ftpsTempSuffix) {
			continue
		}
		stale := dir + "/" + entry.Name
		if stale == currentTemp {
			continue
		}
		_ = sess.conn.Delete(stale)
	}
}

func (s *FTPSRemoteStore) remoteExists(ctx context.Context, c *ftp.ServerConn, remotePath string) (bool, error) {
	_, err := c.FileSize(remotePath)
	if err == nil {
		return true, nil
	}
	if code, ok := ftpsTextCode(err); ok && code == ftp.StatusFileUnavailable {
		return false, nil
	}
	return false, s.classifyErr(ctx, "size", err)
}

func (s *FTPSRemoteStore) verifyRemote(ctx context.Context, sess *ftpsSession, remotePath string, job *model.ContentBackupJob) (ok bool, err error) {
	resp, retrErr := sess.conn.Retr(remotePath)
	if retrErr != nil {
		if code, isProto := ftpsTextCode(retrErr); isProto && code == ftp.StatusFileUnavailable {
			return false, fmt.Errorf("content backup ftps: retr %s: %w (%v)", remotePath, ErrFTPSMissing, retrErr)
		}
		return false, s.classifyErr(ctx, "retr", retrErr)
	}
	defer func() {
		// Response.Close must run or the data connection leaks; its 226 read
		// failure downgrades an otherwise successful verification.
		if closeErr := resp.Close(); closeErr != nil && err == nil {
			ok, err = false, s.classifyErr(ctx, "retr-close", closeErr)
		}
	}()
	hasher := sha256.New()
	n, copyErr := io.Copy(hasher, &ftpsThrottledReader{ctx: ctx, limiter: s.target.Limiter, r: resp})
	if copyErr != nil {
		return false, s.classifyErr(ctx, "retr-read", copyErr)
	}
	if job.CompressedBytes > 0 && n != job.CompressedBytes {
		return false, nil
	}
	return hex.EncodeToString(hasher.Sum(nil)) == job.CompressedSHA256, nil
}

func (s *FTPSRemoteStore) discardTemp(sess *ftpsSession, tempPath string) {
	_ = sess.conn.Delete(tempPath)
}

func (s *FTPSRemoteStore) dial(ctx context.Context, phaseDeadline time.Time) (*ftpsSession, error) {
	return s.dialSession(ctx, phaseDeadline, true)
}

func (s *FTPSRemoteStore) dialSession(ctx context.Context, phaseDeadline time.Time, watch bool) (*ftpsSession, error) {
	tlsConfig := s.buildTLSConfig()
	sess := newFTPSSession()
	if watch {
		sess.watch(ctx)
	}
	dialer := net.Dialer{Timeout: s.target.ConnectTimeout}
	var (
		firstMu sync.Mutex
		first   = true
	)
	dialFunc := func(network, address string) (net.Conn, error) {
		raw, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		_ = raw.SetDeadline(phaseDeadline)
		sess.track(raw)
		firstMu.Lock()
		isControl := first
		first = false
		firstMu.Unlock()
		if isControl {
			// The real target is explicit FTPS on port 21: the library sends
			// AUTH TLS over this plaintext socket and upgrades it afterwards.
			// DialWithTLS (implicit, 990-style) would never connect.
			return raw, nil
		}
		// With a custom dial func the library skips its own TLS wrap for data
		// connections, so wrap here: the data channel is never plaintext.
		return tls.Client(raw, tlsConfig), nil
	}
	controlAddr := net.JoinHostPort(s.target.Host, strconv.Itoa(s.target.Port))
	c, err := ftp.Dial(controlAddr,
		ftp.DialWithDialFunc(dialFunc),
		ftp.DialWithExplicitTLS(tlsConfig),
	)
	if err != nil {
		sess.close()
		return nil, s.classifyErr(ctx, "connect", err)
	}
	sess.conn = c
	if err := c.Login(s.target.Username, s.target.Password); err != nil {
		sess.close()
		return nil, s.classifyErr(ctx, "login", err)
	}
	sess.lastUsed = time.Now()
	return sess, nil
}

func (s *FTPSRemoteStore) buildTLSConfig() *tls.Config {
	pin := s.pin
	return &tls.Config{
		ServerName:         s.target.Host,
		MinVersion:         tls.VersionTLS12,
		ClientSessionCache: s.tlsCache,
		// The strict leaf-SHA256 pin replaces the default CA verification.
		// InsecureSkipVerify also disables the validity-period check, so the
		// callback re-checks NotBefore/NotAfter itself.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("%w: peer presented no certificate", ErrFTPSCertificate)
			}
			leaf, parseErr := x509.ParseCertificate(rawCerts[0])
			if parseErr != nil {
				return fmt.Errorf("%w: cannot parse leaf certificate: %v", ErrFTPSCertificate, parseErr)
			}
			if sum := sha256.Sum256(leaf.Raw); sum != pin {
				return fmt.Errorf("%w: leaf sha256 %s does not match the pinned %s",
					ErrFTPSCertificate, hex.EncodeToString(sum[:]), hex.EncodeToString(pin[:]))
			}
			now := time.Now()
			if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
				return fmt.Errorf("%w: leaf certificate outside its validity window (%s .. %s)",
					ErrFTPSCertificate,
					leaf.NotBefore.UTC().Format(time.RFC3339), leaf.NotAfter.UTC().Format(time.RFC3339))
			}
			return nil
		},
	}
}

func ftpsTextCode(err error) (int, bool) {
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code, true
	}
	return 0, false
}

// classifyErr maps transport failures onto the sentinel classes T05 consumes.
// A cancelled context always wins so lease/shutdown handling stays in T05.
func (s *FTPSRemoteStore) classifyErr(ctx context.Context, phase string, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("content backup ftps: %s aborted: %w (%v)", phase, ctxErr, err)
	}
	sentinel := ftpsSentinelFor(phase, err)
	if sentinel == nil {
		return fmt.Errorf("content backup ftps: %s: %v", phase, err)
	}
	return fmt.Errorf("content backup ftps: %s: %w (%v)", phase, sentinel, err)
}

func ftpsSentinelFor(phase string, err error) error {
	code, proto := ftpsTextCode(err)
	if proto {
		switch phase {
		case "connect":
			if code == ftp.StatusNotAvailable {
				return ErrFTPSNetwork
			}
			// AUTH TLS was refused: never continue on a plaintext channel.
			return ErrFTPSCertificate
		case "login":
			switch {
			case code == ftp.StatusNotLoggedIn || code == ftp.StatusInvalidCredentials:
				return ErrFTPSAuth
			case code == ftp.StatusNotAvailable:
				return ErrFTPSNetwork
			case code >= 500:
				// PBSZ/PROT/TYPE negotiation refused: the protected data
				// channel cannot be established, which is a channel-security
				// failure, not something to silently work around.
				return ErrFTPSCertificate
			default:
				return ErrFTPSNetwork
			}
		default:
			switch {
			case code == ftp.StatusFileUnavailable || code == ftp.StatusExceededStorage ||
				code == ftp.StatusBadFileName || code == ftp.StatusNotLoggedIn:
				return ErrFTPSPermission
			case code == ftp.StatusBadCommand || code == ftp.StatusBadArguments ||
				code == ftp.StatusNotImplemented || code == ftp.StatusBadSequence ||
				code == ftp.StatusNotImplementedParameter:
				return ErrFTPSUnsupported
			case code >= 500:
				return ErrFTPSPermission
			default:
				return ErrFTPSNetwork
			}
		}
	}
	var certVerify *tls.CertificateVerificationError
	if errors.As(err, &certVerify) || errors.Is(err, ErrFTPSCertificate) {
		return ErrFTPSCertificate
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrFTPSTimeout
	}
	return ErrFTPSNetwork
}

func ftpsIsLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// ftpsSession owns one control connection plus every data connection dialed
// for it. The library's Stor/Retr/Rename take no context, so cancellation is
// implemented here: a watcher force-closes all tracked connections when ctx
// is done, which makes the blocked transfer return an error.
type ftpsSession struct {
	conn     *ftp.ServerConn
	lastUsed time.Time

	mu       sync.Mutex
	conns    []net.Conn
	aborted  bool
	stop     chan struct{}
	stopOnce sync.Once
}

func (s *ftpsSession) dead() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aborted
}

// releaseDataConns 丢掉这一轮上传用过的数据连接。ftp 库自己会关它们，但引用留在
// conns 里的话，每传一个文件切片就长一截，setDeadline 每次都要把整条切片扫一遍。
// conns[0] 是控制连接（它最先被拨出来），必须留下。
func (s *ftpsSession) releaseDataConns() {
	s.mu.Lock()
	if len(s.conns) <= 1 {
		s.mu.Unlock()
		return
	}
	data := append([]net.Conn(nil), s.conns[1:]...)
	s.conns = s.conns[:1]
	s.mu.Unlock()
	for _, c := range data {
		_ = c.Close()
	}
}

func newFTPSSession() *ftpsSession {
	return &ftpsSession{stop: make(chan struct{})}
}

func (s *ftpsSession) track(c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.aborted {
		_ = c.Close()
		return
	}
	s.conns = append(s.conns, c)
}

func (s *ftpsSession) abort() {
	s.mu.Lock()
	s.aborted = true
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func (s *ftpsSession) watch(ctx context.Context) {
	s.watchOnce(ctx)
}

// watchOnce aborts the connections if ctx is cancelled. The returned function detaches
// the watch so a later cancel (typical: caller defer cancel after success) cannot kill a
// session that is going back to the pool.
func (s *ftpsSession) watchOnce(ctx context.Context) func() {
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

func (s *ftpsSession) setDeadline(t time.Time) {
	s.mu.Lock()
	conns := append([]net.Conn(nil), s.conns...)
	s.mu.Unlock()
	for _, c := range conns {
		_ = c.SetDeadline(t)
	}
}

func (s *ftpsSession) clearDeadlines() {
	s.setDeadline(time.Time{})
}

func (s *ftpsSession) quit() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Quit()
}

func (s *ftpsSession) close() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.setDeadline(time.Now().Add(ftpsShutdownGrace))
	_ = s.quit()
	s.abort()
}

type ftpsRemoteReader struct {
	sess      *ftpsSession
	resp      *ftp.Response
	body      io.Reader
	closeOnce sync.Once
	closeErr  error
}

func (r *ftpsRemoteReader) Read(p []byte) (int, error) {
	return r.body.Read(p)
}

// Close terminates both the data and the control connection (design doc 9.2)
// and is safe to call more than once.
func (r *ftpsRemoteReader) Close() error {
	r.closeOnce.Do(func() {
		r.sess.setDeadline(time.Now().Add(ftpsShutdownGrace))
		dataErr := r.resp.Close()
		quitErr := r.sess.quit()
		r.sess.close()
		r.closeErr = errors.Join(dataErr, quitErr)
	})
	return r.closeErr
}

// BandwidthLimiter is a simple token bucket. STOR and the verification RETR
// share one instance so the node-wide upload budget from design doc 4.4 holds
// even though verification roughly doubles the bytes on the wire.
type BandwidthLimiter struct {
	bytesPerSecond int64

	mu        sync.Mutex
	available int64
	last      time.Time
}

func NewBandwidthLimiter(bytesPerSecond int64) *BandwidthLimiter {
	if bytesPerSecond <= 0 {
		return nil
	}
	return &BandwidthLimiter{
		bytesPerSecond: bytesPerSecond,
		available:      bytesPerSecond,
		last:           time.Now(),
	}
}

func (l *BandwidthLimiter) Wait(ctx context.Context, n int64) error {
	if l == nil || n <= 0 {
		return nil
	}
	l.mu.Lock()
	now := time.Now()
	l.available += int64(now.Sub(l.last).Seconds() * float64(l.bytesPerSecond))
	if l.available > l.bytesPerSecond {
		l.available = l.bytesPerSecond
	}
	l.last = now
	var wait time.Duration
	if l.available >= n {
		l.available -= n
	} else {
		deficit := n - l.available
		l.available = 0
		wait = time.Duration(float64(deficit) / float64(l.bytesPerSecond) * float64(time.Second))
	}
	l.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type ftpsThrottledReader struct {
	ctx     context.Context
	limiter *BandwidthLimiter
	r       io.Reader
}

func (t *ftpsThrottledReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		if waitErr := t.limiter.Wait(t.ctx, int64(n)); waitErr != nil {
			return n, waitErr
		}
	}
	return n, err
}

type ftpsTrackedReader struct {
	r   io.Reader
	n   int64
	err error
}

func (t *ftpsTrackedReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	t.n += int64(n)
	if err != nil {
		t.err = err
	}
	return n, err
}
